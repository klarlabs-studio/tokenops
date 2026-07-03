package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/infra/readguard"
)

// preToolUseInput is the JSON Claude Code sends a PreToolUse hook on stdin.
type preToolUseInput struct {
	SessionID string `json:"session_id"`
	// AgentID is present only for subagent tool calls; empty for the main
	// agent. It scopes read-guard's ledger per agent-context so a subagent's
	// read never blocks the main agent's later read of the same file.
	AgentID   string `json:"agent_id"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath string          `json:"file_path"`
		Offset   json.RawMessage `json:"offset"`
		Limit    json.RawMessage `json:"limit"`
	} `json:"tool_input"`
}

// hookDecision is the JSON a PreToolUse hook writes to stdout.
type hookDecision struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	} `json:"hookSpecificOutput"`
}

// newReadGuardCmd is the Claude Code Read-dedup hook. Its bare form is the
// hook handler (reads the PreToolUse JSON on stdin, decides allow/deny); the
// `hook` and `stats` subcommands install and inspect it.
func newReadGuardCmd() *cobra.Command {
	var (
		mode string
		dir  string
	)
	cmd := &cobra.Command{
		Use:   "read-guard",
		Short: "Claude Code PreToolUse hook that prevents redundant file re-reads",
		Long: `read-guard is a Claude Code PreToolUse hook. Wired onto the Read
tool, it detects when the agent re-reads the same file in full, unchanged,
within a session — the biggest reclaimable slice of agent context — and (in
active mode) blocks the re-read so those tokens are never spent. It works for
clients that never route through the tokenops proxy (e.g. Claude Code on a
subscription), because it acts inside the client.

Modes: observe (default) logs what it would block without interfering;
active denies redundant unchanged full re-reads. Ranged reads (offset/limit)
are always allowed.

Bare invocation is the hook handler (reads PreToolUse JSON on stdin). Use
'tokenops read-guard hook' to print the settings.json block, and
'tokenops read-guard stats' to see reclamation.`,
		Args:   cobra.NoArgs,
		Hidden: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReadGuardHook(cmd, readguard.ParseMode(mode), dir)
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "observe", "observe (log only) | active (deny redundant re-reads)")
	cmd.Flags().StringVar(&dir, "dir", "", "state/ledger dir (defaults to ~/.tokenops/read-guard)")
	cmd.AddCommand(newReadGuardHookCmd())
	cmd.AddCommand(newReadGuardStatsCmd())
	return cmd
}

// runReadGuardHook reads the PreToolUse JSON, evaluates, and emits the
// decision. Errors never block a tool call — a broken guard must fail open.
// Debug goes to stderr; only decision JSON goes to stdout.
func runReadGuardHook(cmd *cobra.Command, mode readguard.Mode, dir string) error {
	body, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return nil // fail open
	}
	var in preToolUseInput
	if err := json.Unmarshal(body, &in); err != nil {
		return nil
	}
	if in.ToolName != "Read" || in.ToolInput.FilePath == "" {
		return nil // not a Read we handle -> no-op (allow normal flow)
	}
	ranged := len(in.ToolInput.Offset) > 0 || len(in.ToolInput.Limit) > 0
	dec := readguard.Evaluate(dir, in.SessionID, in.AgentID, in.ToolInput.FilePath, ranged, mode, time.Now())
	if !dec.Block {
		return nil // allow: exit 0 with no stdout
	}
	out := hookDecision{}
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = "deny"
	out.HookSpecificOutput.PermissionDecisionReason = dec.Reason
	enc := json.NewEncoder(cmd.OutOrStdout())
	return enc.Encode(out)
}

func newReadGuardHookCmd() *cobra.Command {
	var mode string
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Print the settings.json block to wire read-guard into Claude Code",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m := readguard.ParseMode(mode)
			exe := "tokenops"
			if p, err := os.Executable(); err == nil {
				exe = p
			}
			block := map[string]any{
				"hooks": map[string]any{
					"PreToolUse": []any{
						map[string]any{
							"matcher": "Read",
							"hooks": []any{
								map[string]any{
									"type":    "command",
									"command": exe,
									"args":    []string{"read-guard", "--mode", string(m)},
									"timeout": 10,
								},
							},
						},
					},
				},
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			fmt.Fprintf(cmd.ErrOrStderr(), "# Add this to ~/.claude/settings.json (mode=%s). Start with observe; switch to active when comfortable.\n", m)
			return enc.Encode(block)
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "observe", "observe | active")
	return cmd
}

func newReadGuardStatsCmd() *cobra.Command {
	var (
		dir     string
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show read-guard reclamation (would-block / blocked / tokens)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := readguard.ReadStats(dir)
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(s)
			}
			out := cmd.OutOrStdout()
			if s.Events == 0 {
				fmt.Fprintln(out, "No read-guard activity yet. Install the hook (`tokenops read-guard hook`) and use Claude Code.")
				return nil
			}
			fmt.Fprintf(out, "read-guard — %d reads seen across %d sessions\n", s.Events, s.DistinctSessions)
			fmt.Fprintf(out, "  repeat reads (same file again in a session): %d\n", s.RepeatReads)
			fmt.Fprintf(out, "    ├─ reclaimable (unchanged full re-read): %d · ~%d tokens\n", s.WouldBlock+s.Blocked, s.ReclaimableTok+s.ReclaimedTok)
			fmt.Fprintf(out, "    ├─ post-edit (file changed — not waste):  %d\n", s.RepeatPostEdit)
			fmt.Fprintf(out, "    └─ ranged (intentional partial re-read):  %d\n", s.RepeatRanged)
			fmt.Fprintf(out, "  currently blocked (active mode): %d · ~%d tokens reclaimed\n", s.Blocked, s.ReclaimedTok)
			if s.Blocked == 0 && s.WouldBlock > 0 {
				fmt.Fprintln(out, "\nThose reclaimable re-reads are real waste. Switch the hook to --mode active to reclaim them.")
			} else if s.RepeatReads > 0 && s.WouldBlock+s.Blocked == 0 {
				fmt.Fprintln(out, "\nAll your repeat reads so far were post-edit or ranged — read-guard correctly leaves those alone. Keep observing.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "state/ledger dir (defaults to ~/.tokenops/read-guard)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}
