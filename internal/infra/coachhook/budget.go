package coachhook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/learning"
)

// SessionBudget is the session-budget belief, with its evidence, as the
// coach's surfaces read it.
type SessionBudget = learning.SessionBudgetBelief

// budgetBeliefFile holds the derived session budget beside the ledger.
const budgetBeliefFile = "session-budget.json"

// Where the budget a session is judged against came from.
const (
	budgetDefault    = "default"
	budgetConfigured = "configured"
	budgetDerived    = "derived"
)

// budgetBasis is the budget one session is judged against and the
// phrase that says whose it is.
type budgetBasis struct {
	USD        float64
	Kind       string
	Provenance string
}

// ceiling names the budget in a nudge. Only a budget the operator set is
// "theirs"; a derived one says what it was read off.
func (b budgetBasis) ceiling() string {
	usd := formatUSD(b.USD)
	switch b.Kind {
	case budgetConfigured:
		return "your " + usd + " session budget"
	case budgetDerived:
		return usd + ", " + b.Provenance
	default:
		return "the default " + usd + " session ceiling"
	}
}

// budgetPolicy is the derivation policy for this package's default.
func budgetPolicy() learning.BudgetPolicy { return learning.DefaultBudgetPolicy(DefaultBudgetUSD) }

// sessionBudget resolves the budget a session is judged against. A
// budget the operator set wins. Otherwise it is the belief read off
// their own sessions, re-derived from the ledger once it is a week old,
// and the fixed default while too few sessions are known. Every failure
// falls back to the default: a coach must never break the session.
func sessionBudget(dir string, cfg Config, now time.Time) budgetBasis {
	if cfg.BudgetUSD > 0 && cfg.BudgetUSD != DefaultBudgetUSD {
		return budgetBasis{USD: cfg.BudgetUSD, Kind: budgetConfigured}
	}
	p := budgetPolicy()
	b, ok := ReadSessionBudget(dir)
	if !ok || b.Stale(now, p) {
		var prev *learning.SessionBudgetBelief
		if ok {
			prev = &b
		}
		b = learning.DeriveSessionBudget(sessionCosts(dir), prev, p, now)
		writeSessionBudget(dir, b)
	}
	if b.Source == learning.SourceHistory && b.BudgetUSD > 0 {
		return budgetBasis{USD: b.BudgetUSD, Kind: budgetDerived, Provenance: b.Provenance()}
	}
	return budgetBasis{USD: DefaultBudgetUSD, Kind: budgetDefault}
}

// pinnedBudget is sessionBudget held fixed for the life of one session:
// a derived budget is recorded on the session the first time it is
// used, so a weekly refresh never moves the bar mid-session (which
// would re-fire or skip tiers already latched).
func pinnedBudget(dir string, st *sessionState, cfg Config, now time.Time) budgetBasis {
	b := sessionBudget(dir, cfg, now)
	if b.Kind != budgetDerived {
		return b
	}
	if st.BudgetUSD > 0 {
		return budgetBasis{USD: st.BudgetUSD, Kind: budgetDerived, Provenance: st.BudgetProvenance}
	}
	st.BudgetUSD, st.BudgetProvenance = b.USD, b.Provenance
	return b
}

// sessionCosts reads each session's peak cost and when it was last seen
// from the ledger: the evidence a budget belief is derived from.
func sessionCosts(dir string) []learning.SessionCost {
	f, err := os.Open(filepath.Join(dir, "events.jsonl")) //nolint:gosec // TokenOps' own ledger
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	idx := map[string]int{}
	var out []learning.SessionCost
	dec := json.NewDecoder(f)
	for {
		var e ledgerEvent
		if err := dec.Decode(&e); err != nil {
			break
		}
		i, seen := idx[e.Session]
		if !seen {
			idx[e.Session] = len(out)
			out = append(out, learning.SessionCost{Session: e.Session, USD: e.CumulativeUSD, LastSeen: e.TS})
			continue
		}
		if e.CumulativeUSD > out[i].USD {
			out[i].USD = e.CumulativeUSD
		}
		if e.TS.After(out[i].LastSeen) {
			out[i].LastSeen = e.TS
		}
	}
	return out
}

// ReadSessionBudget reads the session-budget belief last derived, with
// its evidence. ok is false when none has been derived yet; dir "" is
// the default.
func ReadSessionBudget(dir string) (learning.SessionBudgetBelief, bool) {
	var b learning.SessionBudgetBelief
	raw, err := os.ReadFile(filepath.Join(resolveDir(dir), budgetBeliefFile)) //nolint:gosec // TokenOps' own state
	if err != nil || json.Unmarshal(raw, &b) != nil || b.Source == "" {
		return learning.SessionBudgetBelief{}, false
	}
	return b, true
}

// writeSessionBudget records the belief atomically; parallel hooks race
// only to write the same derivation.
func writeSessionBudget(dir string, b learning.SessionBudgetBelief) {
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return
	}
	final := filepath.Join(dir, budgetBeliefFile)
	tmp := fmt.Sprintf("%s.%d.tmp", final, os.Getpid())
	if os.WriteFile(tmp, raw, 0o644) == nil { //nolint:gosec // derived figures, not a secret
		_ = os.Rename(tmp, final)
	}
}
