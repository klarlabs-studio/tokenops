package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	bcap "go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/config"
)

// boundaryTimeout bounds one evaluation. The work is a few file reads;
// anything slower is a stuck disk, and the call goes ahead (fail open).
const boundaryTimeout = 2 * time.Second

// boundaryHookInput is what the pause point reads from a PreToolUse
// payload: ids only. tool_input is never read, so no prompt, path or
// command reaches the boundary engine.
type boundaryHookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	ToolUseID      string `json:"tool_use_id"`
	ToolName       string `json:"tool_name"`
	// Directory is the project the opencode plugin forwards, for the
	// same attribution Claude Code's transcript path gives.
	Directory string `json:"directory"`
}

// newBoundaryGuardCmd is the pause point (ADR 0014): a PreToolUse hook on
// every tool call that checks the continue and spend boundaries.
func newBoundaryGuardCmd(rf *rootFlags) *cobra.Command {
	var client string
	cmd := &cobra.Command{
		Use:   bcap.HookVerb,
		Short: "PreToolUse hook that checks action boundaries before every tool call",
		Long: `boundary-guard is the pause point for action boundaries (ADR 0014).
Wired as a PreToolUse hook on every tool, it evaluates the continue and spend
rules in config.yaml's boundaries section. A rule in shadow is recorded and changes
nothing. An enforced ask rule asks when someone is at the keyboard and
otherwise refuses with the rule and the tokenops approve command; an
enforced deny rule refuses.

It reads ids only, never the tool's input, and fails open: any error or
timeout lets the call through and is recorded.

Install it with 'tokenops hooks install --boundary'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBoundaryGuard(cmd, rf, client)
		},
	}
	cmd.Flags().StringVar(&client, "client", bcap.HarnessClaudeCode, "client that runs the hook: claude-code | opencode")
	return cmd
}

func runBoundaryGuard(cmd *cobra.Command, rf *rootFlags, client string) error {
	body, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 4<<20))
	if err != nil {
		return nil // fail open
	}
	var in boundaryHookInput
	if json.Unmarshal(body, &in) != nil || in.SessionID == "" {
		return nil
	}
	harness := bcap.HarnessClaudeCode
	if strings.EqualFold(client, bcap.HarnessOpencode) {
		harness = bcap.HarnessOpencode
	}
	call := bcap.Call{
		Harness: harness, SessionID: in.SessionID, TranscriptPath: in.TranscriptPath,
		ToolUseID: in.ToolUseID,
	}
	project := bcap.TranscriptProject(in.TranscriptPath)
	if in.Directory != "" {
		project = filepath.Base(in.Directory)
	}
	call.AgentID, call.WorkflowID = bcap.Attribution(harness, in.SessionID, project)
	// opencode's tool.execute.before refuses by throwing and has no
	// prompt to answer, so an ask there is a refusal with the way out.
	env := bcap.Env{Attended: harness == bcap.HarnessClaudeCode && sessionAttended()}
	cfg, err := loadConfig(rf)
	if err != nil {
		bcap.RecordFailure(call, env, fmt.Errorf("config unreadable: %w", err))
		return nil
	}
	res := guardWithTimeout(cmd.Context(), cfg, call, env, bcap.Pause)
	return writeBoundaryVerdict(cmd.OutOrStdout(), res)
}

// guardWithTimeout runs the evaluation, letting the call through when it
// fails or overruns. Both are recorded.
func guardWithTimeout(ctx context.Context, cfg config.Config, call bcap.Call, env bcap.Env,
	eval func([]bcap.Rule, bcap.Call, bcap.Env) (bcap.Result, error)) bcap.Result {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, boundaryTimeout)
	defer cancel()
	type answer struct {
		res bcap.Result
		err error
	}
	done := make(chan answer, 1)
	go func() {
		res, err := eval(cfg.BoundaryRules(), call, env)
		done <- answer{res, err}
	}()
	select {
	case a := <-done:
		if a.err != nil {
			bcap.RecordFailure(call, env, a.err)
			return bcap.Result{}
		}
		return a.res
	case <-ctx.Done():
		bcap.RecordFailure(call, env, errors.New("evaluation timed out"))
		return bcap.Result{}
	}
}

// writeBoundaryVerdict prints the PreToolUse decision, or nothing when the
// call goes ahead untouched.
func writeBoundaryVerdict(w io.Writer, res bcap.Result) error {
	if res.Verdict == bcap.VerdictNone {
		return nil
	}
	out := hookDecision{}
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = res.Verdict
	out.HookSpecificOutput.PermissionDecisionReason = res.Reason
	return json.NewEncoder(w).Encode(out)
}

func newBoundariesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "boundaries",
		Short: "Limits set before work starts: status, promote, demote",
		Long: `Boundaries are rules in config.yaml's boundaries section (ADR 0014): an
action the agent is about to take, the figures that must hold, and what is
then required — allow, ask, approver:<role> or deny.

  boundaries:
    - name: session-spend
      action: continue
      when: { session_cost_usd: ">= 20" }
      require: ask

Every rule starts in shadow: it records what it would have done and binds
nothing. 'status' shows how often each rule matched and, per client, whether
it is enforced or advice only; 'promote' makes a rule bind.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newBoundariesStatusCmd(), newBoundariesStageCmd("promote"), newBoundariesStageCmd("demote"))
	return cmd
}

func newBoundariesStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Each rule's stage, how often it matched, and where it is enforced",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := resolveMutableConfigPath(configFlag(cmd))
			if err != nil {
				return err
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			st, err := bcap.RuleStatuses(cfg.BoundaryRules(), bcap.Env{}, bcap.Wired(cfg))
			if err != nil {
				return err
			}
			if jsonOut {
				return writeControlJSON(cmd, st)
			}
			writeBoundariesStatus(cmd.OutOrStdout(), st)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

func writeBoundariesStatus(w io.Writer, st bcap.Status) {
	if st.Note != "" {
		fmt.Fprintln(w, st.Note)
	}
	for _, r := range st.Rules {
		fmt.Fprintf(w, "\n%s  %s → %s  [%s]\n", r.Name, r.Action, r.Require, r.Stage)
		verb := "matched"
		if r.Stage == "shadow" {
			verb = "would have fired"
		}
		fmt.Fprintf(w, "  %s %d times in %d %s", verb, r.Matches, r.Sessions, scopeNoun(r.Action))
		if r.LastMatched != nil {
			fmt.Fprintf(w, ", last %s", r.LastMatched.Local().Format("2006-01-02 15:04"))
		}
		fmt.Fprintln(w)
		for _, h := range r.Harnesses {
			line := fmt.Sprintf("  %-12s %s", h.Harness, h.Enforcement)
			if h.Note != "" {
				line += " — " + h.Note
			}
			fmt.Fprintln(w, line)
		}
		if r.Note != "" {
			fmt.Fprintf(w, "  note: %s\n", r.Note)
		}
	}
	if len(st.Pending) > 0 {
		fmt.Fprintln(w, "\nWaiting on you:")
		for _, p := range st.Pending {
			fmt.Fprintf(w, "  %s  %s (%s): %s — %s\n", p.ID, p.Rule, p.Require, p.Crossed, p.Approve)
		}
	}
}

// scopeNoun names what a rule's matches are counted across: sessions for
// the agent's actions, routes for the router, destinations for sharing.
func scopeNoun(action string) string {
	switch action {
	case "model_route":
		return "routes"
	case "share":
		return "destinations"
	default:
		return "sessions"
	}
}

func newBoundariesStageCmd(verb string) *cobra.Command {
	change, short := bcap.Promote, "Make a boundary bind: it asks or refuses from the next tool call"
	if verb == "demote" {
		change, short = bcap.Demote, "Put a boundary back in shadow: it records what it would have done and binds nothing"
	}
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   verb + " <name>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveMutableConfigPath(configFlag(cmd))
			if err != nil {
				return err
			}
			res, err := change(path, args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				return writeControlJSON(cmd, res)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is now %s (wrote %s)\n%s\n", res.Rule, res.Stage, res.Config, res.Note)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

// approverAtTerminal reports whether an approval comes from a person: its
// stdin is a terminal. A variable so tests can stand in for one.
var approverAtTerminal = func(in io.Reader) bool { return interactiveInput(in, false) }

func newBoundaryAnswerCmd(verb string) *cobra.Command {
	var session, jsonOut bool
	short := "Let an agent past a boundary that asked: its next step, or the rest of the session"
	if verb == "deny" {
		short = "Refuse what a boundary asked about: the next step, or the rest of the session"
	}
	cmd := &cobra.Command{
		Use:   verb + " <id>",
		Short: short,
		Long: short + `.

The id is the bnd-… one the agent was refused with, or that
'tokenops boundaries status' lists as waiting. The answer is recorded on the
decision with your user name, so 'tokenops explain <id>' shows who answered.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// An agent can run commands too: an approval needs a person at a
			// terminal, so it is refused when stdin is not one.
			if verb == "approve" && !approverAtTerminal(cmd.InOrStdin()) {
				return fmt.Errorf("tokenops approve needs a terminal: run it yourself, not through an agent (an agent can only deny)")
			}
			res, err := bcap.Decide(bcap.Env{}, bcap.DecideRequest{ID: args[0], Decision: verb, Session: session, Method: bcap.MethodCLI})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeControlJSON(cmd, res)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s, %s): %s\n", res.Decision, res.ID, res.Rule, res.Scope, res.Note)
			return nil
		},
	}
	cmd.Flags().BoolVar(&session, "session", false, "answer for the rest of the session, not only the next step")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}
