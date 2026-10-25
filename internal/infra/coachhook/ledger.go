package coachhook

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/learning"
)

// ledgerEvent is one appended coaching record.
type ledgerEvent struct {
	TS            time.Time `json:"ts"`
	Session       string    `json:"session"`
	CumulativeUSD float64   `json:"cumulative_usd"`
	BudgetUSD     float64   `json:"budget_usd"`
	Fraction      float64   `json:"fraction"`
	TierFired     float64   `json:"tier_fired"` // 0 when no tier fired this Stop
	Model         string    `json:"model"`
	// Suppressed names the quiet rule that held a nudge back this Stop,
	// empty when nothing was held.
	Suppressed string `json:"suppressed,omitempty"`
	// Promotion records that this Stop argued the read-guard case.
	Promotion bool `json:"promotion,omitempty"`
	// Compact records that this Stop gave the compact_now tip.
	Compact bool `json:"compact,omitempty"`
	// Unpriced names a model the rate card did not know. Without it a
	// session on an uncatalogued model is indistinguishable in the ledger
	// from one that genuinely cost nothing.
	Unpriced string `json:"unpriced,omitempty"`
	// QuotaWindow and QuotaUsedPct record the live plan window the coach
	// judged against, when one was known; QuotaTier the tier it spoke.
	QuotaWindow  string  `json:"quota_window,omitempty"`
	QuotaUsedPct float64 `json:"quota_used_pct,omitempty"`
	QuotaTier    float64 `json:"quota_tier,omitempty"`
	// QuotaBurn records that this Stop warned the window runs out before
	// its reset.
	QuotaBurn bool `json:"quota_burn,omitempty"`
	// ReadingLost records that this Stop said the plan reading is lost.
	ReadingLost bool `json:"reading_lost,omitempty"`
	// Ambient records that a due tip went to the status line instead of
	// interrupting; Suppressed says why.
	Ambient bool `json:"ambient,omitempty"`
	// Holdout records that a due tip was held back for the holdout and
	// logged as shadow.
	Holdout bool `json:"holdout,omitempty"`
	// StoodDown names the kind of tip the coach did not give because the
	// vendor's own surface already says it, and VendorSurface that
	// surface. Suppressed says "vendor_speaks".
	StoodDown     string `json:"stood_down,omitempty"`
	VendorSurface string `json:"vendor_surface,omitempty"`
}

func appendLedger(dir string, e ledgerEvent) {
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // ledger, not a secret
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_ = json.NewEncoder(f).Encode(e)
}

// Stats summarises the coaching ledger for a `stats` view.
type Stats struct {
	Events           int            `json:"events"`
	DistinctSessions int            `json:"distinct_sessions"`
	Alerts           int            `json:"alerts"`         // total tier firings
	AlertsByTier     map[string]int `json:"alerts_by_tier"` // "50%" -> count, "200%" -> count …
	MaxCumulativeUSD float64        `json:"max_cumulative_usd"`
	TotalEstSpendUSD float64        `json:"total_est_spend_usd"` // sum of each session's peak cumulative
	// Suppressed counts nudges the quiet policy held back, by rule
	// ("min_interval", "max_per_session"). A rate limit you cannot see
	// working is indistinguishable from one that does nothing.
	Suppressed map[string]int `json:"suppressed,omitempty"`
	// PromotionNudges counts the sessions in which the coach argued the
	// read-guard case.
	PromotionNudges int `json:"promotion_nudges,omitempty"`
	// UnpricedModels counts turns per model the rate card could not
	// price. A budget that never moves because nothing could be costed is
	// not a lean session, and this is the only place that difference
	// shows.
	UnpricedModels map[string]int `json:"unpriced_models,omitempty"`
	// QuotaNudges counts quota-window nudges by tier ("75%" -> count),
	// and QuotaWindows the windows they were said about.
	QuotaNudges  map[string]int `json:"quota_nudges,omitempty"`
	QuotaWindows map[string]int `json:"quota_windows,omitempty"`
	// BurnWarnings counts run-out warnings given.
	BurnWarnings int `json:"burn_warnings,omitempty"`
	// RunOut scores the run-out forecasts against what became of each
	// window once it reset.
	RunOut RunOutScore `json:"run_out"`
	// StoodDown counts, by tip kind, the tips the coach did not give
	// because the vendor's own surface says it.
	StoodDown map[string]int `json:"stood_down,omitempty"`
	// CompactTips counts the compact_now tips given.
	CompactTips int `json:"compact_tips,omitempty"`
	// SessionBudget is the budget belief last derived from the
	// operator's own sessions, with its evidence. Nil before the first
	// derivation.
	SessionBudget *learning.SessionBudgetBelief `json:"session_budget,omitempty"`
}

// ReadStats reads the ledger and aggregates it. Cumulative spend is a
// per-event snapshot, so per-session peak (the last/highest cumulative) is the
// session's spend; total est spend sums those peaks across sessions.
func ReadStats(dir string) (Stats, error) {
	dir = resolveDir(dir)
	f, err := os.Open(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return Stats{AlertsByTier: map[string]int{}, RunOut: readRunOuts(dir)}, nil
		}
		return Stats{}, err
	}
	defer func() { _ = f.Close() }()

	s := Stats{AlertsByTier: map[string]int{}, RunOut: readRunOuts(dir)}
	peak := map[string]float64{}
	dec := json.NewDecoder(f)
	for {
		var e ledgerEvent
		if err := dec.Decode(&e); err != nil {
			break
		}
		s.Events++
		if e.CumulativeUSD > peak[e.Session] {
			peak[e.Session] = e.CumulativeUSD
		}
		if e.TierFired > 0 {
			s.Alerts++
			s.AlertsByTier[tierLabel(e.TierFired)]++
		}
		if e.QuotaTier > 0 || e.QuotaBurn {
			if s.QuotaNudges == nil {
				s.QuotaNudges, s.QuotaWindows = map[string]int{}, map[string]int{}
			}
			if e.QuotaBurn {
				s.BurnWarnings++
			} else {
				s.QuotaNudges[tierLabel(e.QuotaTier)]++
			}
			s.QuotaWindows[e.QuotaWindow]++
		}
		if e.StoodDown != "" {
			if s.StoodDown == nil {
				s.StoodDown = map[string]int{}
			}
			s.StoodDown[e.StoodDown]++
		}
		if e.Compact {
			s.CompactTips++
		}
		if e.Promotion {
			s.PromotionNudges++
		}
		if e.Unpriced != "" {
			if s.UnpricedModels == nil {
				s.UnpricedModels = map[string]int{}
			}
			s.UnpricedModels[e.Unpriced]++
		}
		if e.Suppressed != "" {
			if s.Suppressed == nil {
				s.Suppressed = map[string]int{}
			}
			s.Suppressed[e.Suppressed]++
		}
	}
	s.DistinctSessions = len(peak)
	if b, ok := ReadSessionBudget(dir); ok {
		s.SessionBudget = &b
	}
	for _, p := range peak {
		s.TotalEstSpendUSD += p
		if p > s.MaxCumulativeUSD {
			s.MaxCumulativeUSD = p
		}
	}
	return s, nil
}

// tierLabel renders a fired fraction as a percentage label ("50%", "100%",
// "200%") for the stats breakdown.
func tierLabel(frac float64) string {
	return fmt.Sprintf("%d%%", int(math.Round(frac*100)))
}
