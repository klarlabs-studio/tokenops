package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/routeguard"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type agentToolInput struct {
	SessionID      string         `json:"session_id"`
	TranscriptPath string         `json:"transcript_path"`
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
	if report.Effective(config.PowerModels) == config.AutonomyOff {
		return nil
	}
	prompt, _ := in.ToolInput["prompt"].(string)
	description, _ := in.ToolInput["description"].(string)
	requested, _ := in.ToolInput["model"].(string)
	catalog := routeCatalog()
	candidates := cfg.Optimizer.SmartRouting.Models[string(eventschema.ProviderAnthropic)]
	ledger := coachLedger()
	now := time.Now()
	// The agent handing work to a cheaper subagent follows the coach's
	// advice. Read from what it asked for, before any rewrite below, so
	// the coach's own move never counts as the operator following it.
	coachcap.RecordResolutions(ledger, now, routeResolutions(routeguard.ObserveSubagent(
		routeGuardDir(dir), in.SessionID, requested, eventschema.ProviderAnthropic, catalog, candidates)))
	if report.Effective(config.PowerModels) != config.AutonomyAutonomous {
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
