// Package savings is the realized-savings ledger as one capability
// (#656): every intervention writes a row with what it expected to save,
// the evidence it rested on and its decision ID; a small randomized
// holdout of cost and drift tips is logged as shadow; and implicit
// outcomes (a compaction, a commit that landed, the spend that followed)
// turn the two cohorts into a realized saving. The hooks write through
// it, and `tokenops verify`, `tokenops coach stats` and the glance read
// it, so none of them can count a saving differently.
package savings

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/learning"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
)

// Ledger is where the rows are kept.
type Ledger interface {
	Append(entries ...learning.SavingsEntry) error
	Load() ([]learning.SavingsEntry, error)
}

// The cohorts a tip can be in, for adapters that may not name the domain.
const (
	Treated = learning.CohortTreated
	Holdout = learning.CohortHoldout
	Shadow  = learning.CohortShadow
)

// Tip is a coach tip as the ledger records it.
type Tip struct {
	ID      string
	Session string
	Kind    string
	Cohort  learning.Cohort
	// ExpectedUSD and ExpectedTokens are the saving per later turn if the
	// tip is acted on.
	ExpectedUSD    float64
	ExpectedTokens int64
	Evidence       string
	// SessionUSD is the session's spend when the tip was due.
	SessionUSD float64
}

// perTurn is the basis of a tip's expected saving.
const perTurn = "per later turn, if acted on"

// RecordTip writes a tip shown (treated) or held back (holdout).
func RecordTip(l Ledger, now time.Time, t Tip) {
	if l == nil || t.ID == "" {
		return
	}
	_ = l.Append(learning.SavingsEntry{
		Type: learning.SavingsIntervention, ID: t.ID, At: now.UTC(), Session: t.Session,
		Capability: learning.CapabilityOf(t.Kind), Kind: t.Kind, Cohort: t.Cohort,
		ExpectedUSD: t.ExpectedUSD, ExpectedTokens: t.ExpectedTokens, ExpectedBasis: perTurn,
		Evidence: t.Evidence, SessionUSD: t.SessionUSD,
	})
}

// Resolution is the implicit outcome of an earlier intervention.
type Resolution struct {
	ID       string
	Signal   string
	Followed bool
	Evidence string
}

// RecordOutcomes writes what became of earlier interventions.
func RecordOutcomes(l Ledger, now time.Time, rs []Resolution) {
	if l == nil || len(rs) == 0 {
		return
	}
	entries := make([]learning.SavingsEntry, 0, len(rs))
	for _, r := range rs {
		if r.ID == "" {
			continue
		}
		entries = append(entries, learning.SavingsEntry{Type: learning.SavingsOutcome, ID: r.ID, At: now.UTC(),
			Signal: r.Signal, Followed: r.Followed, Evidence: r.Evidence})
	}
	_ = l.Append(entries...)
}

// RecordReadGuard writes a re-read the guard refused, or, in observe
// mode (shadow), would have refused. A refusal is an action boundary and
// is never held back.
func RecordReadGuard(l Ledger, now time.Time, id, session string, shadow bool, tokens int64) {
	if l == nil || id == "" {
		return
	}
	cohort := learning.CohortTreated
	if shadow {
		cohort = learning.CohortShadow
	}
	_ = l.Append(learning.SavingsEntry{
		Type: learning.SavingsIntervention, ID: id, At: now.UTC(), Session: session,
		Capability: learning.CapabilityReadGuard, Kind: "reread", Cohort: cohort,
		ExpectedTokens: tokens, ExpectedBasis: "once, the re-read not made",
		Evidence: fmt.Sprintf("an unchanged file already in context, ~%d tokens", tokens),
	})
}

// RecordRoute writes a suggestion to move work to another model.
func RecordRoute(l Ledger, now time.Time, id, session, kind, from, to string) {
	if l == nil || id == "" {
		return
	}
	_ = l.Append(learning.SavingsEntry{
		Type: learning.SavingsIntervention, ID: id, At: now.UTC(), Session: session,
		Capability: learning.CapabilityRouting, Kind: kind, Cohort: learning.CohortTreated,
		Evidence: kind + " work on " + from + "; suggested " + to,
	})
}

// Deps are where the evidence is read from. Nil fields read nothing.
type Deps struct {
	Ledger Ledger
	// SessionPeaks is each session's API-equivalent spend when last seen.
	SessionPeaks func() map[string]float64
	// Followed is the follow-through ledger's resolution per decision ID.
	Followed func() map[string]bool
	// Commits builds, for interventions since a time, whether a commit
	// landed in the session's repository within a day after one.
	Commits func(ctx context.Context, since, now time.Time) func(session string, at time.Time) (landed, known bool)
}

// Report is the ledger read back.
type Report struct {
	Kinds []learning.KindSavings `json:"kinds"`
	// Interventions counts the delivered interventions, Held the tips
	// held back for the holdout, Shadow the observe-only records.
	Interventions int `json:"interventions"`
	Held          int `json:"held"`
	Shadow        int `json:"shadow,omitempty"`
	// RealizedUSD sums the kinds whose holdout comparison concluded.
	// Unknown while none has.
	RealizedUSD measurement.Value `json:"realized_usd"`
}

// Build reads the ledger and its implicit outcomes. A ledger that cannot
// be read is an empty report.
func Build(ctx context.Context, d Deps, now time.Time) Report {
	r := Report{RealizedUSD: measurement.Unknown("no holdout comparison has concluded yet")}
	if d.Ledger == nil {
		return r
	}
	entries, err := d.Ledger.Load()
	if err != nil || len(entries) == 0 {
		return r
	}
	var implicit learning.Implicit
	if d.SessionPeaks != nil {
		implicit.SessionPeakUSD = d.SessionPeaks()
	}
	if d.Followed != nil {
		implicit.Followed = d.Followed()
	}
	if d.Commits != nil {
		since := now
		for _, e := range entries {
			if e.Type == learning.SavingsIntervention && e.At.Before(since) {
				since = e.At
			}
		}
		implicit.CommitLanded = d.Commits(ctx, since, now)
	}
	r.Kinds = learning.SummarizeSavings(entries, implicit, now)
	var realized []measurement.Value
	for _, k := range r.Kinds {
		r.Interventions += k.Treated.Count
		r.Held += k.Holdout.Count
		r.Shadow += k.Shadow.Count
		if k.RealizedUSD.Known() {
			realized = append(realized, k.RealizedUSD)
		}
	}
	if len(realized) > 0 {
		r.RealizedUSD = measurement.Sum(realized...)
	}
	return r
}

// Holdouts are the kinds some of whose interventions were held back: the
// randomized comparisons.
func (r Report) Holdouts() []learning.KindSavings {
	var out []learning.KindSavings
	for _, k := range r.Kinds {
		if k.HasHoldout() {
			out = append(out, k)
		}
	}
	return out
}

var kindNames = map[string]string{
	"compact_now": "compact tips", "budget_50": "50% session-budget tips", "budget_75": "75% session-budget tips",
	"budget_over": "spent-budget tips", "quota_100": "used-up window tips", "quota_burn": "run-out warnings",
	"reread": "re-reads refused",
}

// Name is how a kind reads in a sentence.
func Name(k learning.KindSavings) string {
	if n, ok := kindNames[k.Kind]; ok {
		return n
	}
	return strings.ReplaceAll(k.Kind, "_", " ") + " (" + strings.ReplaceAll(k.Capability, "_", " ") + ")"
}

// Describe is one kind of intervention in one line: how many were
// delivered and held back, what each was expected to save, and what the
// holdout showed. The CLI and the MCP surfaces print it as is.
func Describe(k learning.KindSavings) string {
	var b strings.Builder
	if k.Capability == learning.CapabilityReadGuard {
		fmt.Fprintf(&b, "%s: %d", Name(k), k.Treated.Count)
	} else {
		fmt.Fprintf(&b, "%s: %d shown", Name(k), k.Treated.Count)
	}
	if k.Holdout.Count > 0 {
		fmt.Fprintf(&b, ", %d held back", k.Holdout.Count)
	}
	if k.Shadow.Count > 0 {
		fmt.Fprintf(&b, ", %d observed only", k.Shadow.Count)
	}
	switch {
	case k.ExpectedPerTipUSD > 0:
		fmt.Fprintf(&b, " · expected ~$%.2f %s", k.ExpectedPerTipUSD, k.ExpectedBasis)
	case k.ExpectedTokens > 0:
		fmt.Fprintf(&b, " · expected ~%s tokens", humanTokens(k.ExpectedTokens))
	}
	if per, ok := k.RealizedPerTipUSD.Amount(); ok {
		after, _ := k.Treated.SpendAfterUSD.Amount()
		held, _ := k.Holdout.SpendAfterUSD.Amount()
		fmt.Fprintf(&b, " · realized $%.2f per tip (spend after: $%.2f shown vs $%.2f held back)", per, after, held)
		return b.String()
	}
	fmt.Fprintf(&b, " · realized %s", k.RealizedPerTipUSD.Caveat())
	return b.String()
}

func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprint(n)
}
