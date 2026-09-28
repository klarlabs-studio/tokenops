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

// delegationRecord is one subagent the coach moved, kept for `tokenops
// coach` and for measuring whether the move stood.
type delegationRecord struct {
	TS      time.Time `json:"ts"`
	Session string    `json:"session"`
	Kind    string    `json:"kind"`
	From    string    `json:"from"`
	To      string    `json:"to"`
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
	if report.Effective(config.PowerModels) != config.AutonomyAutonomous {
		return nil
	}
	prompt, _ := in.ToolInput["prompt"].(string)
	description, _ := in.ToolInput["description"].(string)
	requested, _ := in.ToolInput["model"].(string)
	dec := routeguard.EvaluateSubagent(routeguard.SubagentInput{
		Description: description, Prompt: prompt, Requested: requested,
		SessionModel: latestTranscriptModel(in.TranscriptPath),
		Provider:     eventschema.ProviderAnthropic, Catalog: routeCatalog(),
		Candidates: cfg.Optimizer.SmartRouting.Models[string(eventschema.ProviderAnthropic)],
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
	recordDelegation(dir, delegationRecord{TS: time.Now().UTC(), Session: in.SessionID, Kind: string(dec.Kind), From: dec.From, To: dec.To})
	return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
}

func delegationLedger(dir string) string {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".tokenops", "route-guard")
	}
	return filepath.Join(dir, "delegations.jsonl")
}

func recordDelegation(dir string, r delegationRecord) {
	path := delegationLedger(dir)
	if path == "" || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	b, _ := json.Marshal(r)
	_, _ = f.Write(append(b, '\n'))
}

// countDelegations reads how many subagents the coach has moved.
func countDelegations(dir string) int {
	b, err := os.ReadFile(delegationLedger(dir))
	if err != nil {
		return 0
	}
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}
