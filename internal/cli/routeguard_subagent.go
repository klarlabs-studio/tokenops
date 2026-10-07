package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/routers"

	"github.com/spf13/cobra"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/routeguard"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type agentToolInput struct {
	SessionID      string         `json:"session_id"`
	TranscriptPath string         `json:"transcript_path"`
	ToolUseID      string         `json:"tool_use_id"`
	ToolInput      map[string]any `json:"tool_input"`
}

type subagentHookOutput struct {
	HookSpecificOutput struct {
		HookEventName            string         `json:"hookEventName"`
		PermissionDecision       string         `json:"permissionDecision"`
		PermissionDecisionReason string         `json:"permissionDecisionReason,omitempty"`
		UpdatedInput             map[string]any `json:"updatedInput"`
	} `json:"hookSpecificOutput"`
	SystemMessage string `json:"systemMessage,omitempty"`
}

// runSubagentGuard handles Claude Code's PreToolUse on the Agent tool.
// When coach.models is effectively autonomous and the subagent's work fits
// a cheaper model, it rewrites the subagent's model (updatedInput) and
// allows the call. Anything else, or any failure, leaves the call alone.
func runSubagentGuard(cmd *cobra.Command, body []byte, dir string) error {
	var in agentToolInput
	if json.Unmarshal(body, &in) != nil || in.ToolInput == nil {
		return nil
	}
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return nil
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil
	}
	report := coachcap.Build(cfg)
	requested, _ := in.ToolInput["model"].(string)
	catalog := routeCatalog(cfg)
	// The model policy is the operator's rule, not the coach's advice, so
	// it holds whatever the coach's dials say.
	if pol := routeguard.EnforcePolicy(routeguard.PolicyInput{
		Requested: requested, SessionModel: latestTranscriptModel(in.TranscriptPath),
		Provider: eventschema.ProviderAnthropic, Catalog: catalog,
		Offered: cfg.Optimizer.SmartRouting.Models[string(eventschema.ProviderAnthropic)],
		Policy:  cfg.ModelPolicy.Policy(),
	}); pol.Forbidden {
		return enforceModelPolicy(cmd, in, report, pol)
	}
	if report.Effective(config.PowerModels) == config.AutonomyOff || routers.Decides(routers.Sources{}, "", true) {
		return nil
	}
	prompt, _ := in.ToolInput["prompt"].(string)
	description, _ := in.ToolInput["description"].(string)
	candidates := cfg.RoutingCandidates(eventschema.ProviderAnthropic)
	ledger := coachLedger()
	now := time.Now()
	// The agent handing work to a cheaper subagent follows the coach's
	// advice. Read from what it asked for, before any rewrite below, so
	// the coach's own move never counts as the operator following it.
	stateDir := routeGuardDir(dir)
	coachcap.RecordResolutions(ledger, now, routeResolutions(routeguard.ObserveSubagent(
		stateDir, in.SessionID, requested, eventschema.ProviderAnthropic, catalog, candidates)))
	coachcap.RecordResolutions(ledger, now, proposalResolutions(routeguard.SettleProposals(stateDir, in.SessionID, in.TranscriptPath)))
	rung := report.Effective(config.PowerModels)
	if rung == config.AutonomyAsk {
		return askSubagentMove(cmd, in, report, stateDir, ledger, now, routeguard.SubagentInput{
			Description: description, Prompt: prompt, Requested: requested,
			SessionModel: latestTranscriptModel(in.TranscriptPath),
			Provider:     eventschema.ProviderAnthropic, Catalog: catalog, Candidates: candidates,
		})
	}
	if rung != config.AutonomyAutonomous {
		return nil
	}
	dec := routeguard.EvaluateSubagent(routeguard.SubagentInput{
		Description: description, Prompt: prompt, Requested: requested,
		SessionModel: latestTranscriptModel(in.TranscriptPath),
		Provider:     eventschema.ProviderAnthropic, Catalog: catalog,
		Candidates: candidates,
	})
	if !dec.Rewrite {
		return nil
	}
	updated := make(map[string]any, len(in.ToolInput))
	for k, v := range in.ToolInput {
		updated[k] = v
	}
	updated["model"] = dec.ToAlias

	out := subagentHookOutput{}
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = "allow"
	out.HookSpecificOutput.PermissionDecisionReason = "tokenops: " + dec.Reason
	out.HookSpecificOutput.UpdatedInput = updated
	switch report.Verbosity {
	case config.VerbosityQuiet:
	case config.VerbosityVerbose:
		out.SystemMessage = fmt.Sprintf("tokenops: moved a subagent from %s to %s: %s. To stop: `tokenops coach set models advise`.",
			dec.From, dec.To, dec.Reason)
	default:
		out.SystemMessage = fmt.Sprintf("tokenops: moved a subagent from %s to %s (%s work).", dec.From, dec.To, dec.Kind)
	}
	coachcap.RecordMove(ledger, now, coachcap.NewID(), in.SessionID, config.PowerModels, string(dec.Kind), dec.From, dec.To)
	return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
}

// enforceModelPolicy keeps a subagent off a model the policy rules out:
// it moves the call to the closest permitted model, or refuses it with
// the reason when none can take the work, so the agent can choose again.
func enforceModelPolicy(cmd *cobra.Command, in agentToolInput, report coachcap.Report, pol routeguard.PolicyDecision) error {
	out := subagentHookOutput{}
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecisionReason = "tokenops: " + pol.Reason
	if pol.ToAlias == "" {
		out.HookSpecificOutput.PermissionDecision = "deny"
		return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
	}
	updated := make(map[string]any, len(in.ToolInput))
	for k, v := range in.ToolInput {
		updated[k] = v
	}
	updated["model"] = pol.ToAlias
	out.HookSpecificOutput.PermissionDecision = "allow"
	out.HookSpecificOutput.UpdatedInput = updated
	if report.Verbosity != config.VerbosityQuiet {
		out.SystemMessage = fmt.Sprintf("tokenops: ran a subagent on %s instead of %s, which your model policy rules out.", pol.To, pol.From)
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
}

// routeGuardDir is the route guard's per-session state directory.
func routeGuardDir(dir string) string {
	if dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".tokenops", "route-guard")
}

// sessionAttended reports whether someone is at the keyboard to answer a
// permission prompt. Claude Code sets CLAUDE_CODE_SESSION_ATTENDED to 1
// in interactive sessions and 0 headless; anything else is treated as
// unattended, because an ask nobody can answer is a denial the agent
// retries until it runs out of turns.
func sessionAttended() bool { return os.Getenv("CLAUDE_CODE_SESSION_ATTENDED") == "1" }

// askSubagentMove proposes moving a subagent to a cheaper model through
// Claude Code's permission prompt (models: ask). The prompt shows the
// rewritten Agent call, so what is approved is exactly what runs. A call
// the operator declined runs as planned when the agent retries it, and
// nothing is asked when nobody is attending or when the operator keeps
// declining this kind of move.
func askSubagentMove(cmd *cobra.Command, in agentToolInput, report coachcap.Report, dir string, ledger coachcap.Ledger, now time.Time, sub routeguard.SubagentInput) error {
	if !sessionAttended() || in.ToolUseID == "" {
		return nil
	}
	call := routeguard.CallKey(sub.Description, sub.Prompt)
	if routeguard.WasDeclined(dir, in.SessionID, call) {
		return nil
	}
	dec := routeguard.EvaluateSubagent(sub)
	if !dec.Rewrite {
		return nil
	}
	if report.Verbosity != config.VerbosityVerbose && coachcap.Quieter(ledger, now)(config.PowerModels, string(dec.Kind)) {
		return nil
	}
	updated := make(map[string]any, len(in.ToolInput))
	for k, v := range in.ToolInput {
		updated[k] = v
	}
	updated["model"] = dec.ToAlias
	out := subagentHookOutput{}
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = "ask"
	// Claude Code's prompt shows this reason under the Agent call, and the
	// call itself only by its description, so the reason carries the
	// proposal (verified in 2.1.284).
	out.HookSpecificOutput.PermissionDecisionReason = fmt.Sprintf(
		"tokenops: run this subagent on %s instead of %s? Its work looks like %s. No pauses the agent; tell it to continue and it runs as planned.",
		dec.To, dec.From, dec.Kind)
	out.HookSpecificOutput.UpdatedInput = updated
	if report.Verbosity == config.VerbosityVerbose {
		// Claude Code shows this after the prompt is answered, not with
		// it; the prompt itself carries the proposal.
		out.SystemMessage = fmt.Sprintf("tokenops: proposed running this subagent on %s instead of %s: %s. To stop asking: `tokenops coach set models advise`.",
			dec.To, dec.From, dec.Reason)
	}
	id := coachcap.NewID()
	routeguard.Propose(dir, in.SessionID, routeguard.Proposal{
		ID: id, ToolUseID: in.ToolUseID, Call: call, Kind: string(dec.Kind), From: dec.From, To: dec.To,
	})
	coachcap.RecordApproval(ledger, now, id, in.SessionID, config.PowerModels, string(dec.Kind), dec.From, dec.To)
	return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
}

func proposalResolutions(outcomes []routeguard.ProposalOutcome) []coachcap.Resolution {
	out := make([]coachcap.Resolution, 0, len(outcomes))
	for _, o := range outcomes {
		evidence := "declined at the prompt"
		if o.Approved {
			evidence = "approved at the prompt"
		}
		out = append(out, coachcap.Resolution{ID: o.ID, Kind: o.Kind, Followed: o.Approved, Evidence: evidence})
	}
	return out
}
