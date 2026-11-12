package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/attribution"
	bcap "go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
)

// boundaryTimeout bounds one evaluation. The work is a few file reads;
// anything slower is a stuck disk, and the call goes ahead (fail open).
const boundaryTimeout = 2 * time.Second

// boundaryHookInput is what the pause point reads from a PreToolUse
// payload: ids, the tool's name, its input and the working directory. The
// input is only hashed, here, for the loop breaker (bcap.HashToolInput),
// and a shell command is read for the agents it starts
// (bcap.PlansStartedBy), then dropped; the working directory is compared
// with folder conditions and dropped: no prompt, path or command reaches
// the boundary engine's files.
type boundaryHookInput struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	ToolUseID      string          `json:"tool_use_id"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	// Cwd is the session's working directory, as Claude Code sends it.
	Cwd string `json:"cwd"`
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

It reads ids and the tool's name; the tool's input is hashed for the loop
breaker and never kept. A shell command that starts another agent (codex,
gemini, cursor-agent, copilot) also reads that agent's plan headroom.

A check that cannot decide (an unreadable config, a timeout) lets the call
through. A rule that matched keeps its verdict even when the record of it
cannot be written. Either failure is recorded, shown by 'tokenops
boundaries status', and on Claude Code said to the operator at the call.

Install it with 'tokenops hooks install --boundary'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBoundaryGuard(cmd, rf, client)
		},
	}
	cmd.Flags().StringVar(&client, "client", bcap.HarnessClaudeCode, "client that runs the hook: claude-code | opencode | codex")
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
	switch {
	case strings.EqualFold(client, bcap.HarnessOpencode):
		harness = bcap.HarnessOpencode
	case strings.EqualFold(client, bcap.HarnessCodex):
		harness = bcap.HarnessCodex
	}
	call := bcap.Call{
		Harness: harness, SessionID: in.SessionID, TranscriptPath: in.TranscriptPath,
		ToolUseID: in.ToolUseID, ToolName: in.ToolName,
	}
	if in.ToolInput != nil {
		call.ArgsHash = bcap.HashToolInput(in.ToolInput)
		if strings.EqualFold(in.ToolName, "Bash") {
			// A shell call that starts another agent spends its plan.
			var bash struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(in.ToolInput, &bash) == nil {
				call.Spends = bcap.PlansStartedBy(bash.Command, harness)
			}
		}
	}
	switch harness {
	case bcap.HarnessClaudeCode:
		// A budget on another provider's bill is not this session's.
		call.Biller = attribution.ClaudeCodeSessionBiller(os.Getenv, in.TranscriptPath, "")
	case bcap.HarnessCodex:
		// Codex runs on the OpenAI plan; its transcripts sit by date,
		// not by project, so the project is the working directory.
		call.Provider, call.Biller = "openai", "openai"
	}
	project := bcap.TranscriptProject(in.TranscriptPath)
	call.Folder = in.Cwd
	switch {
	case in.Directory != "":
		project = filepath.Base(in.Directory)
		call.Folder = in.Directory
	case harness == bcap.HarnessCodex:
		project = filepath.Base(in.Cwd)
	}
	call.AgentID, call.WorkflowID = bcap.Attribution(harness, in.SessionID, project)
	// opencode's tool.execute.before refuses by throwing, and Codex's
	// PreToolUse takes deny but not ask: neither has a prompt to answer,
	// so an ask there is a refusal with the way out.
	env := bcap.Env{Attended: harness == bcap.HarnessClaudeCode && sessionAttended()}
	cfg, err := loadConfig(rf)
	if err != nil {
		// With no rules to read nothing can be held; the operator is told
		// here, since no rule is left to say so.
		cause := fmt.Errorf("config unreadable: %w", err)
		bcap.RecordFailure(call, env, bcap.Result{}, cause)
		return writeBoundaryVerdict(cmd.OutOrStdout(), bcap.Result{}, failureNote(harness, bcap.Result{}, cause))
	}
	env.Approval = cfg.ApprovalMode()
	res, failed := guardWithTimeout(cmd.Context(), cfg, call, env, bcap.Pause)
	if harness == bcap.HarnessCodex && res.Verdict == bcap.VerdictAsk {
		// Codex rejects permissionDecision "ask" as unsupported and runs
		// the call: an ask that reached here would let it through.
		res.Verdict = bcap.VerdictDeny
	}
	return writeBoundaryVerdict(cmd.OutOrStdout(), res, failureNote(harness, res, failed))
}

// failureNote is a failed check as a line for the operator, on the
// harness whose hook output has a place for one. The ledger the failure
// is also recorded in may be what failed, so this is the copy that cannot
// be lost with it. Codex and opencode read the verdict alone, and their
// output is left as it was.
func failureNote(harness string, res bcap.Result, failed error) string {
	if harness != bcap.HarnessClaudeCode {
		return ""
	}
	return bcap.FailureNote(res, failed)
}

// guardWithTimeout runs the evaluation and returns its verdict with what
// went wrong, if anything; both are recorded. A check that fails or
// overruns before it decided lets the call through. One that decided and
// then failed to record it keeps its verdict.
func guardWithTimeout(ctx context.Context, cfg config.Config, call bcap.Call, env bcap.Env,
	eval func([]bcap.Rule, bcap.Call, bcap.Env) (bcap.Result, error)) (bcap.Result, error) {
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
		res, err := eval(bcap.AllRules(cfg), call, env)
		done <- answer{res, err}
	}()
	select {
	case a := <-done:
		bcap.RecordFailure(call, env, a.res, a.err)
		return a.res, a.err
	case <-ctx.Done():
		err := errors.New("evaluation timed out")
		bcap.RecordFailure(call, env, bcap.Result{}, err)
		return bcap.Result{}, err
	}
}

// writeBoundaryVerdict prints the PreToolUse decision, with note as a
// line for the operator, or nothing when the call goes ahead untouched
// and there is nothing to say.
func writeBoundaryVerdict(w io.Writer, res bcap.Result, note string) error {
	if res.Verdict == bcap.VerdictNone && note == "" {
		return nil
	}
	out := hookDecision{SystemMessage: note}
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
nothing. On its own, or as 'status', it shows how often each rule matched
and, per client, whether it is enforced or advice only; 'promote' makes a
rule bind.

'tokenops init' writes the headroom pause and the loop breaker on a new
config, in shadow; on an older one, 'headroom-pause' and 'loop-breaker'
add them.

What boundaries are: they stop mistakes and drift,
not an agent that sets out to get past them. The pause point, the rules
and the record of answers run as your user, so an agent with an
unrestricted shell can remove the pause point from its harness or edit
the files. With a presence key
enforced ('tokenops boundaries presence'), an approval proves who approved
it; it does not prove an agent could not have gone around the rule. See
'tokenops approve --help' for what an approval guarantees.`,
		Args: cobra.NoArgs,
	}
	// On its own it answers what the daily command is asked: the rules and
	// where each stands. It printed this help.
	status := newBoundariesStatusCmd()
	cmd.RunE = status.RunE
	cmd.Flags().AddFlag(status.Flags().Lookup("json"))
	cmd.AddCommand(status, newBoundariesStageCmd("promote"), newBoundariesStageCmd("demote"), newBoundariesHeadroomPauseCmd(),
		newBoundariesLoopBreakerCmd(), newBoundariesPresenceCmd(), newBoundariesExtraCmd())
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
			st, err := bcap.RuleStatuses(bcap.AllRules(cfg), bcap.Env{Approval: cfg.ApprovalMode()}, bcap.Wired(cfg))
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
		if when := conditionsLine(r.When); when != "" {
			fmt.Fprintf(w, "  when %s\n", when)
		}
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
	if st.Presence != nil {
		fmt.Fprintf(w, "\napproval: %s\n", st.Presence.Note)
	}
	if f := st.Failed; f != nil {
		fmt.Fprintf(w, "\n%d boundary check(s) went wrong in the last day, last %s: %s\n",
			f.Count, f.LastAt.Local().Format("2006-01-02 15:04"), f.Last)
	}
	for _, e := range st.Extras {
		fmt.Fprintf(w, "\nextra allowance until %s: %s lifted by %s", e.Until.Local().Format("15:04"), strings.Join(e.Rules, ", "), e.Granter)
		if e.Why != "" {
			fmt.Fprintf(w, " (%s)", e.Why)
		}
		switch e.Presence {
		case bcap.PresenceSigned:
			fmt.Fprintf(w, ", presence-signed with key %s", e.PresenceKey)
		case bcap.PresenceRefused:
			fmt.Fprintf(w, ": NOT honoured, it lifts nothing (%s)", e.Ignored)
		}
		fmt.Fprintln(w)
	}
	if len(st.Pending) > 0 {
		fmt.Fprintln(w, "\nWaiting on you:")
		for _, p := range st.Pending {
			fmt.Fprintf(w, "  %s  %s (%s): %s — %s\n", p.ID, p.Rule, p.Require, p.Crossed, p.Approve)
		}
	}
}

// conditionsLine joins a rule's conditions, saying of each time window
// whether it holds now.
func conditionsLine(when []bcap.ConditionStatus) string {
	parts := make([]string, len(when))
	for i, c := range when {
		parts[i] = c.Condition
		switch {
		case c.HoldsNow == nil:
		case *c.HoldsNow:
			parts[i] += " (now: inside)"
		default:
			parts[i] += " (now: outside, so the rule does not apply)"
		}
	}
	return strings.Join(parts, "; ")
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

// newBoundariesHeadroomPauseCmd adds the headroom pause to a config made
// before init wrote it: asked for, never added on its own.
func newBoundariesHeadroomPauseCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "headroom-pause",
		Short: "Add the headroom pause: ask before the next tool call once the tightest plan window is down to 10% left, in shadow",
		Long: `headroom-pause adds the boundary a new 'tokenops init' writes:

  - name: headroom-pause
    action: continue
    when: { plan_left_pct: "<= 10" }
    require: ask
    stage: shadow

Once the tightest plan window TokenOps knows is down to 10% left, the
agent's next tool call asks first. It starts in shadow: it records each session it would
have paused and binds nothing until 'tokenops boundaries promote
headroom-pause'. It also installs the pause point (the boundary hook) in
Claude Code, opencode and Codex where they are installed. A config that already
has a continue rule on plan_left_pct is left as it is.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := resolveMutableConfigPath(configFlag(cmd))
			if err != nil {
				return err
			}
			res, err := bcap.AddHeadroomPause(path)
			if err != nil {
				return err
			}
			var hooks []string
			for _, client := range []string{hookClientClaudeCode, hookClientOpencode, hookClientCodex} {
				if !clientInstalled(client) {
					continue
				}
				var buf bytes.Buffer
				if err := installClientHooks(&buf, client, "", hookSelection{boundary: true}, false, coachhook.DefaultBudgetUSD); err != nil {
					hooks = append(hooks, client+": "+err.Error())
					continue
				}
				hooks = append(hooks, client+": pause point installed")
			}
			if jsonOut {
				return writeControlJSON(cmd, struct {
					bcap.PauseAdded
					Hooks []string `json:"hooks"`
				}{res, hooks})
			}
			out := cmd.OutOrStdout()
			if res.Added {
				fmt.Fprintf(out, "%s is set, in shadow (wrote %s)\n%s\n", res.Rule, res.Config, res.Note)
			} else {
				fmt.Fprintf(out, "%s [%s]: %s\n", res.Rule, res.Stage, res.Note)
			}
			for _, h := range hooks {
				fmt.Fprintf(out, "  %s\n", h)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

// newBoundariesLoopBreakerCmd adds the loop breaker to a config made
// before init wrote it.
func newBoundariesLoopBreakerCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "loop-breaker",
		Short: "Add the loop breaker: ask before the next tool call once the agent repeats itself with no file changed, in shadow",
		Long: `loop-breaker adds the boundary a new 'tokenops init' writes:

  - name: loop-breaker
    action: continue
    when: { loop_calls: ">= 4" }
    require: ask
    stage: shadow

loop_calls counts the agent's latest tool calls that went round a loop with
no file changed: the same call (the same tool with the same input) again
and again, or the same cycle of two or three calls at least three times.
An edit or write it has not seen is progress and starts the count again.
The pause point keeps each call's tool name and a hash of its input, never
the input. Only Claude Code's hook sends the input, so the rule is
evaluated there only.

It starts in shadow: it records each session it would have paused and binds
nothing until 'tokenops boundaries promote loop-breaker'. A config that
already has a continue rule on loop_calls is left as it is.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := resolveMutableConfigPath(configFlag(cmd))
			if err != nil {
				return err
			}
			res, err := bcap.AddLoopBreaker(path)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeControlJSON(cmd, res)
			}
			out := cmd.OutOrStdout()
			if res.Added {
				fmt.Fprintf(out, "%s is set, in shadow (wrote %s)\n%s\n", res.Rule, res.Config, res.Note)
			} else {
				fmt.Fprintf(out, "%s [%s]: %s\n", res.Rule, res.Stage, res.Note)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
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

// approveLimits is what approval guarantees, said where it is used: a
// guardrail against an agent's own tools, not a boundary against a process
// running as the operator.
const approveLimits = `

What this guarantees: an agent cannot approve through TokenOps' MCP tools
(they can only deny), the daemon's decision endpoint approves only with a
presence signature, this command refuses to run when stdin is not a
terminal, and every answer is recorded with how it was given (cli, mcp,
api, prompt) and, when signed, with which presence key.

With a presence key enrolled ('tokenops boundaries presence enroll'), this
command asks for Touch ID, a paired Apple Watch or your password and signs
the approval; with boundary_approval: presence, an approval without that
signature is not honoured, so an approval through 'script -q /dev/null
tokenops approve <id>' or a hand-written answer file is not honoured. A paired Watch is
offered every dialog: approve only a dialog you started, and read what it
names (rule, action, session).

What it does not: without presence enforced, the terminal check only asks
whether stdin is a terminal. A process running as you can get one (script
-q /dev/null tokenops approve <id>), read the daemon's token in ~/.tokenops
and call its API, or write the boundary ledger. Even with it, a process
running as you can edit config.yaml (until rules are sealed), replace
TokenOps' binaries (a replaced key is held 15 minutes and announced), or
remove the pause point from the harness's settings, after which no rule
is checked at all.

Recommended: do not give agents an unrestricted shell for tokenops. Have
your harness ask before 'tokenops approve', 'tokenops mode', 'tokenops
routing', 'script' and curl to the daemon (in Claude Code, permission
rules such as Bash(tokenops approve:*) set to ask).`

// approverAtTerminal reports whether an approval comes from a person: its
// stdin is a terminal. A variable so tests can stand in for one.
var approverAtTerminal = func(in io.Reader) bool { return interactiveInput(in, false) }

// confirmAnswer shows the decision waiting on id and asks for a yes. Only
// y or yes answers; anything else, or no answer, leaves it waiting.
func confirmAnswer(cmd *cobra.Command, verb, id string, session bool) (bool, error) {
	p, err := bcap.Waiting(bcap.Env{}, id)
	if err != nil {
		return false, err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Boundary %q (%s) asked at %s:\n  %s\n  from %s, session %s\n",
		p.Rule, p.Require, p.At.Local().Format("15:04"), p.Crossed, p.Harness, p.SessionID)
	scope := "the next step only"
	if session {
		scope = "the rest of the session (at most 24 hours)"
	}
	fmt.Fprintf(out, "%s it for %s? [y/N] ", strings.ToUpper(verb[:1])+verb[1:], scope)
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	fmt.Fprintln(out, "Left waiting; nothing was answered.")
	return false, nil
}

func newBoundaryAnswerCmd(verb string) *cobra.Command {
	var session, jsonOut, confirm bool
	short := "Let an agent past a boundary that asked: its next step, or the rest of the session"
	if verb == "deny" {
		short = "Refuse what a boundary asked about: the next step, or the rest of the session"
	}
	long := short + `.

The id is the bnd-… one the agent was refused with, or that
'tokenops boundaries status' lists as waiting. The answer is recorded on the
decision with your user name, so 'tokenops explain <id>' shows who answered.`
	if verb == "approve" {
		long += approveLimits
	}
	cmd := &cobra.Command{
		Use:   verb + " <id>",
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// An agent can run commands too: an approval needs a person at a
			// terminal, so it is refused when stdin is not one.
			if verb == "approve" && !approverAtTerminal(cmd.InOrStdin()) {
				return fmt.Errorf("tokenops approve needs a terminal: run it yourself, not through an agent (an agent can only deny)")
			}
			if confirm {
				ok, err := confirmAnswer(cmd, verb, args[0], session)
				if err != nil || !ok {
					return err
				}
			}
			env, err := presenceEnv(cmd)
			if err != nil {
				return err
			}
			req := bcap.DecideRequest{ID: args[0], Decision: verb, Session: session, Method: bcap.MethodCLI}
			// A deny only tightens and is never asked to be signed.
			if verb == "approve" {
				if req.Proof, err = presenceProofFor(cmd, env, args[0], session); err != nil {
					return err
				}
			}
			res, err := bcap.Decide(env, req)
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
	cmd.Flags().BoolVar(&confirm, "confirm", false, "show what is waiting and ask before answering (the menu bar opens a terminal with this)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}
