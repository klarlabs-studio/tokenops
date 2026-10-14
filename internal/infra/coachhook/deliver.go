package coachhook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

// Why a tip went to the status line instead of interrupting, or was not
// said at all, as the ledger records it.
const (
	// SuppressedEpisode: a further step over the budget in a session
	// that was already told it is over. Said once per episode; the rest
	// wait on the status line.
	SuppressedEpisode = "once_per_episode"
	// SuppressedDailyBudget: the day's interruptions are spent, so the
	// tip waits on the status line.
	SuppressedDailyBudget = "interruption_budget"
	// SuppressedSnoozed and SuppressedAnswered: the operator snoozed the
	// tip's family, or answered a tip of it in this session.
	SuppressedSnoozed  = ft.HeldSnoozed
	SuppressedAnswered = ft.HeldEpisode
)

// interruptionsFile counts the day's in-session interruptions across
// sessions.
const interruptionsFile = "interruptions.json"

type interruptions struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

// spoken is how the session's quiet counters stood before this Stop
// decided anything, so a tip sent to the status line does not count
// as having spoken.
type spoken struct {
	nudges int
	last   string
}

// deliver decides where a tip that is due is said. Most interrupt the
// session; two go to the status line instead, as the open tip: a further
// step over the budget in a session already told it is over, and any
// tip past the day's interruption budget, unless it is about work about
// to stop. Either way the tip is offered, latched and recorded once.
func (c Config) deliver(dir string, dec *Decision, st *sessionState, before spoken, prevMaxFired float64, now time.Time) {
	if !dec.Nudge {
		return
	}
	kind := tipKind(*dec)
	switch {
	case kind == "":
	case dec.FiredFraction > 1.0+fracEpsilon && prevMaxFired >= 1.0-fracEpsilon:
		toStatusLine(dec, st, before, SuppressedEpisode)
		return
	case c.Quiet.MaxPerDay > 0 && quietable(kind) && readInterruptions(dir, now).Count >= c.Quiet.MaxPerDay:
		toStatusLine(dec, st, before, SuppressedDailyBudget)
		return
	}
	countInterruption(dir, now)
}

// toStatusLine turns a spoken tip into an ambient one.
func toStatusLine(dec *Decision, st *sessionState, before spoken, reason string) {
	dec.Nudge, dec.Ambient, dec.Suppressed = false, true, reason
	st.Nudges, st.LastNudgeAt = before.nudges, before.last
}

func dayOf(now time.Time) string { return now.Local().Format("2006-01-02") }

func readInterruptions(dir string, now time.Time) interruptions {
	var n interruptions
	b, err := os.ReadFile(filepath.Join(dir, interruptionsFile)) //nolint:gosec // TokenOps' own state
	if err != nil || json.Unmarshal(b, &n) != nil || n.Day != dayOf(now) {
		return interruptions{Day: dayOf(now)}
	}
	return n
}

// countInterruption adds one to the day's count. Parallel sessions may
// lose an update, which costs at most one extra interruption.
func countInterruption(dir string, now time.Time) {
	n := readInterruptions(dir, now)
	n.Count++
	b, err := json.Marshal(n)
	if err != nil {
		return
	}
	final := filepath.Join(dir, interruptionsFile)
	tmp := fmt.Sprintf("%s.%d.tmp", final, os.Getpid())
	if os.WriteFile(tmp, b, 0o644) == nil { //nolint:gosec // a counter, not a secret
		_ = os.Rename(tmp, final)
	}
}
