package readguard

import (
	"fmt"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
)

// A read-only command (git status, git diff, ls, grep, …) run again with
// nothing changed since has its answer in the agent's context already.
// The hook sees every Bash call, so a command that may change things
// resets the window like an edit; what it cannot see is a change made
// outside the session, by the operator or another process. So a repeat
// counts only within RepeatWithin and within the last LoopWindow tool
// calls, in the same working directory, and the refusal says how to run
// it anyway.
//
// It follows the read guard's mode: observe records what it would have
// answered, active refuses the repeat with a pointer to the earlier run.
// On the machine it was built on 1 in 296 read-only commands repeated
// with nothing changed, so it is cheap insurance, not a big saving.

// RepeatWithin bounds how long ago the earlier run may have been.
const RepeatWithin = 10 * time.Minute

// CommandRepeat is a read-only command run again with nothing changed
// since: how many tool calls ago it ran and how long ago.
type CommandRepeat struct {
	Command  string
	CallsAgo int
	Ago      time.Duration
	// ranAt is the earlier run's time in unix seconds.
	ranAt int64
}

// BashCall reduces a Bash command to the call the window keeps: a
// read-only command, or an edit for anything that may change things.
func BashCall(command, cwd string, at time.Time) Call {
	if norm, ok := agentdx.ReadOnlyCommand(command); ok {
		return Call{Kind: CallCommand, Command: norm, Cwd: cwd, At: at}
	}
	return Call{Kind: CallEdit, At: at}
}

// repeatOf finds c's earlier run in recent (which does not hold c yet):
// the same command in the same directory, with no edit or changing
// command after it, within RepeatWithin of c.
func repeatOf(recent []loopEntry, c Call) *CommandRepeat {
	if c.Kind != CallCommand {
		return nil
	}
	key := c.countKey()
	for i := len(recent) - 1; i >= 0; i-- {
		e := recent[i]
		if e.Kind == CallEdit {
			return nil
		}
		if e.Kind != CallCommand || e.Key != key {
			continue
		}
		if e.At == 0 || c.At.IsZero() {
			return nil
		}
		ago := c.At.Sub(time.Unix(e.At, 0))
		if ago < 0 || ago > RepeatWithin {
			return nil
		}
		return &CommandRepeat{Command: c.Command, CallsAgo: len(recent) - i, Ago: ago, ranAt: e.At}
	}
	return nil
}

// Reason is the refusal the agent reads: what ran, when, and how to run
// it anyway when something changed outside the session.
func (r CommandRepeat) Reason() string {
	cmd := r.Command
	if len(cmd) > maxPatternRunes {
		cmd = string([]rune(cmd)[:maxPatternRunes]) + "…"
	}
	return fmt.Sprintf("tokenops read-guard: `%s` ran %s ago (%s), and nothing has changed since that this session made; "+
		"its output is above. If something changed outside the session, run it again with a flag or path added.",
		cmd, roundAgo(r.Ago), callsAgo(r.CallsAgo))
}

func roundAgo(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func callsAgo(n int) string {
	if n == 1 {
		return "the previous tool call"
	}
	return fmt.Sprintf("%d tool calls ago", n)
}

// RecordRepeat appends a repeated command to the ledger: blocked in
// active mode, would-block in observe. The command itself is not kept.
func RecordRepeat(dir, sessionID, agentID string, mode Mode, now time.Time) {
	action := ActionWouldBlock
	if mode == ModeActive {
		action = ActionBlocked
	}
	appendLedger(resolveDir(dir), ledgerEvent{TS: now.UTC(), Mode: mode, Session: sessionID, Agent: agentID,
		Kind: kindCommand, Action: action})
}

const kindCommand = "command"
