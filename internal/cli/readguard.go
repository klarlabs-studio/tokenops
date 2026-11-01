package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/config"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/infra/readguard"
)

// preToolUseInput is the JSON Claude Code sends a PreToolUse hook on stdin.
type preToolUseInput struct {
	SessionID string `json:"session_id"`
	// AgentID is present only for subagent tool calls; empty for the main
	// agent. It scopes read-guard's ledger per agent-context so a subagent's
	// read never blocks the main agent's later read of the same file.
	AgentID string `json:"agent_id"`
	// TranscriptPath is the session's transcript, where a compaction
	// shows up.
	TranscriptPath string `json:"transcript_path"`
	// Cwd is the session's working directory, which a loop tip shortens
	// paths against.
	Cwd       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath string          `json:"file_path"`
		Offset   json.RawMessage `json:"offset"`
		Limit    json.RawMessage `json:"limit"`
		// Pattern, Path, Glob and Type are a Grep's or Glob's input.
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Glob    string `json:"glob"`
		Type    string `json:"type"`
	} `json:"tool_input"`
}

// hookDecision is the JSON a PreToolUse hook writes to stdout.
//
// PermissionDecision is omitted when the hook only adds context: an
// "allow" there would skip the operator's permission prompt, which a
// note to the agent has no business doing.
type hookDecision struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision,omitempty"`
		PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
		AdditionalContext        string `json:"additionalContext,omitempty"`
	} `json:"hookSpecificOutput"`
}

// newReadGuardCmd is the Claude Code Read-dedup hook. Its bare form is the
// hook handler (reads the PreToolUse JSON on stdin, decides allow/deny); the
// `hook` and `stats` subcommands install and inspect it.
func newReadGuardCmd(rf *rootFlags) *cobra.Command {
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

Modes: observe logs what it would block without interfering; active denies
redundant unchanged full re-reads. Ranged reads (offset/limit) are always
allowed. With no --mode, the guard follows coaching.delivery in config.yaml:
intervene means active, anything else means observe. Pass --mode to pin one
regardless of config.

It also gives the agent a short note, never a refusal, when it goes round
in circles: the same file read three times in its last ten tool calls, or
the same Grep or Glob run three times with no edit in between. The note
names the file or the search and is given once per file or search per
session, at the tool call itself. It follows the coach: nothing is said
when coach.inform is off or verbosity is quiet.

Bare invocation is the hook handler (reads PreToolUse JSON on stdin). Use
'tokenops read-guard hook' to print the settings.json block, and
'tokenops coach stats' to see reclamation.`,
		Args:   cobra.NoArgs,
		Hidden: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReadGuardHook(cmd, resolveGuardMode(cmd, rf, mode), dir, loopTipGate(rf))
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "", "observe (log only) | active (deny redundant re-reads); default follows coaching.delivery")
	cmd.Flags().StringVar(&dir, "dir", "", "state/ledger dir (defaults to ~/.tokenops/read-guard)")
	cmd.AddCommand(newReadGuardHookCmd())
	return cmd
}

// runReadGuardHook reads the PreToolUse JSON, evaluates, and emits the
// decision. Errors never block a tool call — a broken guard must fail open.
// Debug goes to stderr; only decision JSON goes to stdout.
//
// Besides Read it sees Grep, Glob and the edit tools, for the loop tips
// (readguard.ObserveCall): a note to the agent when it re-reads one file
// or repeats one search. speak says whether the coach may give one; a
// tip never changes whether a read is allowed.
func runReadGuardHook(cmd *cobra.Command, mode readguard.Mode, dir string, speak func(kind string) bool) error {
	body, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return nil // fail open
	}
	var in preToolUseInput
	if err := json.Unmarshal(body, &in); err != nil {
		return nil
	}
	now := time.Now()
	call, ok := loopCall(in)
	if !ok {
		return nil // not a tool we handle -> no-op (allow normal flow)
	}
	var dec readguard.Decision
	if call.Kind == readguard.CallRead {
		dec = evaluateRead(in, mode, dir, now)
	}
	tip, told := readguard.ObserveCall(dir, in.SessionID, in.AgentID, call, speak)
	note := ""
	if told {
		note = tip.Message()
		coachcap.RecordTip(coachLedger(), now, coachcap.NewID(), in.SessionID, tip.Kind, coachcap.ViaAgent)
	}
	if !dec.Block && note == "" {
		return nil // allow: exit 0 with no stdout
	}
	out := hookDecision{}
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	if dec.Block {
		out.HookSpecificOutput.PermissionDecision = "deny"
		out.HookSpecificOutput.PermissionDecisionReason = dec.Reason
	}
	out.HookSpecificOutput.AdditionalContext = note
	enc := json.NewEncoder(cmd.OutOrStdout())
	return enc.Encode(out)
}

// evaluateRead is the read guard's verdict on one Read.
func evaluateRead(in preToolUseInput, mode readguard.Mode, dir string, now time.Time) readguard.Decision {
	ranged := len(in.ToolInput.Offset) > 0 || len(in.ToolInput.Limit) > 0
	if in.AgentID == "" {
		readguard.ForgetCompacted(dir, in.SessionID, in.TranscriptPath)
	}
	dec := readguard.Evaluate(dir, in.SessionID, in.AgentID, in.ToolInput.FilePath, ranged, mode, now)
	if dec.Action == readguard.ActionBlocked || dec.Action == readguard.ActionWouldBlock {
		// A refusal is an action boundary: recorded with its expected
		// saving, never held back. Observe mode's would-block is shadow.
		savings.RecordReadGuard(savings.DefaultLedger(), now, coachcap.NewID(), in.SessionID,
			dec.Action == readguard.ActionWouldBlock, dec.EstTokens)
	}
	return dec
}

// loopCall reduces a PreToolUse payload to the call the loop detector
// keeps, or false for a tool the hook does not handle.
func loopCall(in preToolUseInput) (readguard.Call, bool) {
	ti := in.ToolInput
	switch in.ToolName {
	case "Read":
		if ti.FilePath == "" {
			return readguard.Call{}, false
		}
		rng := ""
		if len(ti.Offset) > 0 || len(ti.Limit) > 0 {
			rng = string(ti.Offset) + ":" + string(ti.Limit)
		}
		return readguard.Call{Kind: readguard.CallRead, Path: ti.FilePath, Range: rng, Cwd: in.Cwd}, true
	case "Grep", "Glob":
		if strings.TrimSpace(ti.Pattern) == "" {
			return readguard.Call{}, false
		}
		return readguard.Call{
			Kind: readguard.CallSearch, Tool: in.ToolName, Pattern: ti.Pattern, Path: ti.Path,
			Glob: ti.Glob, Type: ti.Type, Cwd: in.Cwd,
		}, true
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		return readguard.Call{Kind: readguard.CallEdit}, true
	}
	return readguard.Call{}, false
}

// loopTipGate says whether the coach may give a loop tip of a kind: the
// inform power is not off, verbosity is not quiet (a loop is not work
// about to stop, the only tips quiet still speaks), and the kind has not
// been quieted by being ignored. The config is read only when a tip is
// due, so the hook stays cheap on every other call. An unreadable config
// reads as the defaults, as for the Stop hook's tips.
func loopTipGate(rf *rootFlags) func(kind string) bool {
	return func(kind string) bool {
		cfg, err := loadConfig(rf)
		if err != nil {
			cfg = config.Default()
		}
		report := coachcap.Build(cfg)
		if report.Effective(config.PowerInform) == config.AutonomyOff || report.Verbosity == config.VerbosityQuiet {
			return false
		}
		return !coachcap.Quieter(coachLedger(), time.Now())(config.PowerInform, kind)
	}
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
			entry := map[string]any{
				"type":    "command",
				"command": exe,
				"args":    []string{"read-guard", "--mode", string(m)},
				"timeout": 10,
			}
			block := map[string]any{
				"hooks": map[string]any{
					"PreToolUse": []any{
						map[string]any{
							"matcher": "Read",
							"hooks":   []any{entry},
						},
						map[string]any{
							"matcher": loopTipMatcher,
							"hooks":   []any{entry},
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

// writeReadGuardStats prints what the read guard saw: repeat reads, the
// reclaimable ones, and those it refused.
func writeReadGuardStats(out io.Writer, s readguard.Stats) {
	if s.Events == 0 {
		fmt.Fprintln(out, "No read-guard activity yet. Install the hook (`tokenops read-guard hook`) and use Claude Code.")
		return
	}
	fmt.Fprintf(out, "Re-reads — %d file reads across %d sessions\n", s.Events, s.DistinctSessions)
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
}

// resolveGuardMode decides whether the guard blocks or only observes.
// An explicit --mode always wins: someone naming it on the command line
// has said what they want. Otherwise coaching.delivery governs, so the
// one config key moves every coaching channel together and takes effect
// without re-registering the hook.
//
// A config that cannot be read resolves to observe rather than failing.
// This is a hook in the agent's critical path, and the whole package
// fails open on principle: a guard that cannot read its settings must
// not start refusing the agent's reads.
func resolveGuardMode(cmd *cobra.Command, rf *rootFlags, flagMode string) readguard.Mode {
	if cmd.Flags().Changed("mode") {
		return readguard.ParseMode(flagMode)
	}
	cfg, err := loadConfig(rf)
	if err != nil {
		return readguard.ModeObserve
	}
	if coachcap.Build(cfg).Effective(config.PowerWaste) == config.AutonomyAutonomous {
		return readguard.ModeActive
	}
	return readguard.ModeObserve
}
