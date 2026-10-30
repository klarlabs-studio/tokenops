package coachhook

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
)

// The operator's spend budgets speak through the same tip lifecycle as
// every other finding: latched, held by a snooze or an answer, and past
// the day's interruption budget sent to the status line. Each window's
// warning, critical and spent tiers are said once per window instance,
// across sessions, because a budget window spans sessions.

// spendFamily is the tip family of the budget window tips.
const spendFamily = "spend"

// spendOverKind is the tip that a budget window is spent.
const spendOverKind = spendFamily + "_over"

// spendLatchFile records, per window instance, the highest tier said.
const spendLatchFile = "spend-budgets.json"

// spendKind names a budget window tip: spend_<pct> for a threshold,
// spend_over at the limit.
func spendKind(tierPct float64) string {
	if tierPct >= 100 {
		return spendOverKind
	}
	return fmt.Sprintf("%s_%d", spendFamily, int(math.Round(tierPct)))
}

func spendLatchKey(r boundaryledger.BudgetReading) string {
	return r.Key + "|" + r.WindowStart.UTC().Format(time.RFC3339)
}

func loadSpendLatches(dir string) map[string]float64 {
	m := map[string]float64{}
	if b, err := os.ReadFile(filepath.Join(dir, spendLatchFile)); err == nil { //nolint:gosec // TokenOps' own state
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// saveSpendLatches keeps the latches of the windows still current, so the
// file holds one entry per configured window.
func saveSpendLatches(dir string, m map[string]float64, readings []boundaryledger.BudgetReading) {
	keep := map[string]float64{}
	for _, r := range readings {
		if v, ok := m[spendLatchKey(r)]; ok {
			keep[spendLatchKey(r)] = v
		}
	}
	if b, err := json.Marshal(keep); err == nil {
		_ = os.WriteFile(filepath.Join(dir, spendLatchFile), b, 0o600)
	}
}

// spendTier is the highest tier r has reached that is above said, or 0.
// Quiet speaks only at the limit.
func spendTier(r boundaryledger.BudgetReading, said float64, verbosity string) float64 {
	best := 0.0
	for _, tier := range []float64{r.WarnPct, r.CritPct, 100} {
		if tier <= 0 || tier > r.UsedPct+fracEpsilon || tier <= said+fracEpsilon {
			continue
		}
		if verbosity == verbosityQuiet && tier < 100 {
			continue
		}
		best = max(best, tier)
	}
	return best
}

// evaluateSpendBudgets says at most one thing about the budget windows
// when nothing else was said this Stop: the window that reached the
// highest new tier, among those the session's turns count toward. A
// window on another provider's bill waits, latch untouched, for a
// session that provider bills.
func evaluateSpendBudgets(dir string, dec *Decision, st *sessionState, cfg Config, model string, now time.Time) {
	if !cfg.Enabled || dec.Nudge || len(cfg.Budgets) == 0 {
		return
	}
	latches := loadSpendLatches(dir)
	defer saveSpendLatches(dir, latches, cfg.Budgets)
	biller := ""
	if cfg.SessionBiller != nil {
		biller = cfg.SessionBiller(model)
	}
	var (
		pick boundaryledger.BudgetReading
		tier float64
	)
	for _, r := range cfg.Budgets {
		if !r.CountsFor(biller) {
			continue
		}
		if t := spendTier(r, latches[spendLatchKey(r)], cfg.Verbosity); t > tier {
			pick, tier = r, t
		}
	}
	if tier == 0 {
		return
	}
	kind := spendKind(tier)
	reason, retry := cfg.hold(kind, st, now)
	switch {
	case reason == "":
		dec.Nudge = true
		dec.SpendKind = kind
		dec.spend = pick
		dec.Message = spendMessage(pick, now)
		st.LastNudgeAt = now.UTC().Format(time.RFC3339Nano)
		st.Nudges++
	case retry:
		if dec.Suppressed == "" {
			dec.Suppressed = reason
		}
		return
	default:
		if dec.Suppressed == "" {
			dec.Suppressed = reason
		}
	}
	latches[spendLatchKey(pick)] = tier
}

// spendLabel names a window the way a person says it: "team daily".
func spendLabel(r boundaryledger.BudgetReading) string {
	return r.Budget + " " + r.Window
}

func spendMessage(r boundaryledger.BudgetReading, now time.Time) string {
	scope := r.Window
	if r.Provider != "" {
		scope += ", billed by " + r.Provider
	}
	msg := fmt.Sprintf("tokenops: budget %s (%s): %s; resets in %s.",
		r.Budget, scope, r.LeftWords(), formatSpan(r.ResetsAt.Sub(now)))
	switch {
	case r.Rule != "" && r.Enforced && r.Require == "deny":
		msg += fmt.Sprintf(" At its limit the boundary %s refuses further steps.", r.Rule)
	case r.Rule != "" && r.Enforced:
		msg += fmt.Sprintf(" At its limit the boundary %s asks before each further step.", r.Rule)
	case r.Rule != "":
		msg += fmt.Sprintf(" Its hard limit is in shadow and stops nothing yet; `tokenops boundaries promote %s` makes it bind.", r.Rule)
	case r.UsedPct >= 100:
		msg += " This budget only notifies; nothing is stopped."
	}
	return msg
}

// shortSpend words a budget window tip for the status line; a window that
// has since reset says nothing.
func (t Tip) shortSpend(now time.Time) string {
	if !t.ResetsAt.IsZero() && !t.ResetsAt.After(now) {
		return ""
	}
	if t.Kind == spendOverKind {
		return t.Window + " budget spent"
	}
	return t.Window + " budget " + leftOfStep(strings.TrimPrefix(t.Kind, spendFamily+"_")) + " left"
}
