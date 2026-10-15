package learning

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/intervention"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
)

// The realized-savings ledger (#656) separates what an intervention was
// expected to save from what it was shown to save. Expected is the
// coach's estimate at the moment it acted; realized is the difference a
// randomized holdout measured afterwards. FinOps calls the first
// potential savings and only counts the second.

// Cohort says whether an intervention was delivered.
type Cohort string

// The cohorts.
const (
	// CohortTreated is an intervention that reached the operator or was
	// performed: a tip shown, a re-read refused, a route suggested.
	CohortTreated Cohort = "treated"
	// CohortHoldout is a tip that was due and randomly held back, logged
	// as shadow: the comparison group the holdout exists to create.
	CohortHoldout Cohort = "holdout"
	// CohortShadow is an intervention recorded but not delivered because
	// the capability itself only observes (the read guard in observe
	// mode). It is not randomized and is never compared as a holdout.
	CohortShadow Cohort = "shadow"
)

// The capabilities whose interventions the ledger records.
const (
	CapabilityCompaction    = "compaction"
	CapabilitySessionBudget = "session_budget"
	CapabilityPlanWindow    = "plan_window"
	CapabilityReadGuard     = "read_guard"
	CapabilityRouting       = "routing"
)

// CapabilityOf names the capability a coach tip kind belongs to.
func CapabilityOf(kind string) string {
	switch family(kind) {
	case "compact":
		return CapabilityCompaction
	case "budget":
		return CapabilitySessionBudget
	case "quota", "reading":
		return CapabilityPlanWindow
	}
	return kind
}

func family(kind string) string {
	f, _, _ := strings.Cut(kind, "_")
	return f
}

// SavingsEntryType is what one ledger line records.
type SavingsEntryType string

// The entry types.
const (
	// SavingsIntervention is something TokenOps did or would have done,
	// with what it expected to save.
	SavingsIntervention SavingsEntryType = "intervention"
	// SavingsOutcome is an implicit outcome observed for an earlier
	// intervention, by its ID.
	SavingsOutcome SavingsEntryType = "outcome"
)

// The implicit signals an outcome entry carries.
const (
	// SignalCompacted: the context fell to half or less, by the operator.
	SignalCompacted = "compacted"
	// SignalAutoCompacted: the client compacted on its own at its ceiling,
	// which is the compaction the tip exists to avoid.
	SignalAutoCompacted = "auto_compacted"
	// SignalModelSwitched: the session moved to another model.
	SignalModelSwitched = "model_switched"
	// SignalUnchanged: nothing changed in the turns that followed.
	SignalUnchanged = "unchanged"
	// SignalSuperseded: a later tip arrived before anything changed.
	SignalSuperseded = "superseded"
)

// SavingsEntry is one ledger line. Only derived figures are kept: no
// prompt, no file content, no path.
type SavingsEntry struct {
	Type SavingsEntryType `json:"type"`
	// ID is the decision ID: the follow-through offer ID for a tip, so the
	// two ledgers join on it.
	ID         string    `json:"id"`
	At         time.Time `json:"at"`
	Session    string    `json:"session,omitempty"`
	Capability string    `json:"capability,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Cohort     Cohort    `json:"cohort,omitempty"`
	// ExpectedUSD and ExpectedTokens are the estimate made when acting;
	// ExpectedBasis says what it is per ("per turn", "once").
	ExpectedUSD    float64 `json:"expected_usd,omitempty"`
	ExpectedTokens int64   `json:"expected_tokens,omitempty"`
	ExpectedBasis  string  `json:"expected_basis,omitempty"`
	// Evidence is the observation the decision rested on, in figures.
	Evidence string `json:"evidence,omitempty"`
	// SessionUSD is the session's API-equivalent spend at the moment, the
	// baseline the spend after the intervention is measured from.
	SessionUSD float64 `json:"session_usd,omitempty"`
	// Signal and Followed are an outcome entry's implicit outcome.
	Signal   string `json:"signal,omitempty"`
	Followed bool   `json:"followed,omitempty"`
}

// The holdout's bounds. It withholds help, so it stays small.
const (
	DefaultHoldoutPercent = 10
	MaxHoldoutPercent     = 10
)

// HoldoutEligible reports whether a tip kind may be held back. Only tips
// about cost and drift qualify; anything about work about to stop (a
// plan window running out or used up, the session budget spent) and any
// action boundary always speaks. It is an allow list, so a new kind is
// safe until someone decides otherwise.
func HoldoutEligible(kind string) bool {
	switch kind {
	case "budget_50", "budget_75", "compact_now":
		return true
	}
	return false
}

// InHoldout assigns a session's tips of a family to the holdout with
// probability percent/100 (clamped to MaxHoldoutPercent). The assignment
// hashes the session and the tip family, so it is fixed for the whole
// episode and independent of anything the outcome could depend on.
func InHoldout(session, kind string, percent int) bool {
	if session == "" || percent <= 0 || !HoldoutEligible(kind) {
		return false
	}
	percent = min(percent, MaxHoldoutPercent)
	h := fnv.New32a()
	_, _ = h.Write([]byte("holdout|" + family(kind) + "|" + session))
	return int(h.Sum32()%100) < percent
}

// Implicit carries the outcomes observed outside the ledger.
type Implicit struct {
	// SessionPeakUSD is each session's API-equivalent spend when last
	// seen; the spend after an intervention is the peak minus SessionUSD.
	SessionPeakUSD map[string]float64
	// Followed is the follow-through ledger's resolution of a shown tip,
	// by decision ID: true followed, false not. Absent is unresolved.
	Followed map[string]bool
	// CommitLanded reports whether a commit landed in the session's
	// repository within a day after at, and whether that is known.
	CommitLanded func(session string, at time.Time) (landed, known bool)
}

// CohortStats is one cohort of one kind of intervention.
type CohortStats struct {
	Count int `json:"count"`
	// Followed of Resolved interventions saw the implicit change the tip
	// asks for (a compaction, a model switch).
	Followed int `json:"followed"`
	Resolved int `json:"resolved"`
	// SpendAfterUSD is the mean API-equivalent spend in the session after
	// the intervention.
	SpendAfterUSD measurement.Value `json:"spend_after_usd"`
	// CommitsLanded of CommitsKnown sessions had a commit land within a
	// day after the intervention.
	CommitsLanded int `json:"commits_landed"`
	CommitsKnown  int `json:"commits_known"`
}

// KindSavings is the ledger's evidence for one kind of intervention.
type KindSavings struct {
	Capability string `json:"capability"`
	Kind       string `json:"kind"`
	// Treated, Holdout: the randomized cohorts. Shadow is the
	// observe-only records, which are not compared; ShadowTokens their
	// expected tokens.
	Treated      CohortStats `json:"treated"`
	Holdout      CohortStats `json:"holdout"`
	Shadow       CohortStats `json:"shadow"`
	ShadowTokens int64       `json:"shadow_tokens,omitempty"`
	// ExpectedPerTipUSD is the mean expected saving per delivered
	// intervention, per ExpectedBasis; ExpectedTokens the sum of the
	// expected tokens.
	ExpectedPerTipUSD float64 `json:"expected_per_tip_usd,omitempty"`
	ExpectedBasis     string  `json:"expected_basis,omitempty"`
	ExpectedTokens    int64   `json:"expected_tokens,omitempty"`
	// Comparison and Verdict are the holdout against the treated cohort:
	// randomized by construction.
	Comparison intervention.Comparison `json:"-"`
	Verdict    intervention.Verdict    `json:"verdict"`
	// RealizedPerTipUSD is what one delivered intervention saved, as the
	// holdout measured it; RealizedUSD that times the delivered count.
	// Unknown until the comparison concludes.
	RealizedPerTipUSD measurement.Value `json:"realized_per_tip_usd"`
	RealizedUSD       measurement.Value `json:"realized_usd"`
}

// HasHoldout reports whether any intervention of the kind was held back.
func (k KindSavings) HasHoldout() bool { return k.Holdout.Count > 0 }

type savingsKey struct{ capability, kind string }

// SummarizeSavings folds the ledger into per-kind evidence, ordered by
// capability then kind.
func SummarizeSavings(entries []SavingsEntry, implicit Implicit, now time.Time) []KindSavings {
	outcomes := map[string]SavingsEntry{}
	for _, e := range entries {
		if e.Type == SavingsOutcome {
			if _, ok := outcomes[e.ID]; !ok {
				outcomes[e.ID] = e
			}
		}
	}
	type cohortRows struct{ treated, holdout, shadow []SavingsEntry }
	groups := map[savingsKey]*cohortRows{}
	var order []savingsKey
	for _, e := range entries {
		if e.Type != SavingsIntervention || e.At.After(now) {
			continue
		}
		k := savingsKey{e.Capability, e.Kind}
		g, ok := groups[k]
		if !ok {
			g = &cohortRows{}
			groups[k] = g
			order = append(order, k)
		}
		switch e.Cohort {
		case CohortHoldout:
			g.holdout = append(g.holdout, e)
		case CohortShadow:
			g.shadow = append(g.shadow, e)
		default:
			g.treated = append(g.treated, e)
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].capability != order[j].capability {
			return order[i].capability < order[j].capability
		}
		return order[i].kind < order[j].kind
	})
	out := make([]KindSavings, 0, len(order))
	for _, k := range order {
		g := groups[k]
		ks := KindSavings{Capability: k.capability, Kind: k.kind}
		ks.Shadow, _ = cohortStats(g.shadow, outcomes, Implicit{})
		for _, r := range g.shadow {
			ks.ShadowTokens += r.ExpectedTokens
		}
		var treatedOutcomes, holdoutOutcomes intervention.Outcomes
		ks.Treated, treatedOutcomes = cohortStats(g.treated, outcomes, implicit)
		ks.Holdout, holdoutOutcomes = cohortStats(g.holdout, outcomes, implicit)
		ks.ExpectedPerTipUSD, ks.ExpectedBasis, ks.ExpectedTokens = expected(g.treated)
		ks.Comparison = intervention.Comparison{
			Assignment:           intervention.Randomised,
			Baseline:             ks.Holdout.SpendAfterUSD,
			Intervention:         ks.Treated.SpendAfterUSD,
			Samples:              min(ks.Treated.Count, ks.Holdout.Count),
			BaselineOutcomes:     holdoutOutcomes,
			InterventionOutcomes: treatedOutcomes,
			At:                   now,
		}
		ks.Verdict = intervention.Judge(ks.Comparison)
		ks.RealizedPerTipUSD, ks.RealizedUSD = realized(ks)
		out = append(out, ks)
	}
	return out
}

func cohortStats(rows []SavingsEntry, outcomes map[string]SavingsEntry, implicit Implicit) (CohortStats, intervention.Outcomes) {
	s := CohortStats{Count: len(rows)}
	var o intervention.Outcomes
	var spend []measurement.Value
	for _, r := range rows {
		if out, ok := outcomes[r.ID]; ok {
			s.Resolved++
			if out.Followed {
				s.Followed++
			}
		} else if followed, ok := implicit.Followed[r.ID]; ok {
			s.Resolved++
			if followed {
				s.Followed++
			}
		}
		if peak, ok := implicit.SessionPeakUSD[r.Session]; ok && peak >= r.SessionUSD {
			spend = append(spend, measurement.Measured(peak-r.SessionUSD, "coach_hook_ledger"))
		} else {
			spend = append(spend, measurement.Unknown("the session's spend after the intervention was not seen"))
		}
		landed, known := false, false
		if implicit.CommitLanded != nil {
			landed, known = implicit.CommitLanded(r.Session, r.At)
		}
		switch {
		case !known:
			o.Unknown++
		case landed:
			s.CommitsKnown++
			s.CommitsLanded++
			o.Achieved++
		default:
			s.CommitsKnown++
			o.NotAchieved++
		}
	}
	s.SpendAfterUSD = meanOf(spend)
	return s, o
}

// meanOf averages the known values; a cohort none of whose spend was seen
// is unknown, and partial coverage is said.
func meanOf(vs []measurement.Value) measurement.Value {
	if len(vs) == 0 {
		return measurement.Unknown("the cohort is empty")
	}
	var total float64
	var known int64
	for _, v := range vs {
		if a, ok := v.Amount(); ok {
			total += a
			known++
		}
	}
	if known == 0 {
		return measurement.Unknown("no session's spend after the intervention was seen")
	}
	return measurement.Measured(total/float64(known), "coach_hook_ledger").Covering(known, int64(len(vs))-known)
}

func expected(rows []SavingsEntry) (perTip float64, basis string, tokens int64) {
	var usd float64
	var n int
	for _, r := range rows {
		tokens += r.ExpectedTokens
		if r.ExpectedUSD > 0 {
			usd += r.ExpectedUSD
			n++
			basis = r.ExpectedBasis
		}
	}
	if n == 0 {
		return 0, "", tokens
	}
	return usd / float64(n), basis, tokens
}

// holdoutMinSamples is the fewest interventions each cohort needs before
// intervention.Judge concludes anything; it mirrors that package's floor
// so the caveat can say so in the holdout's words.
const holdoutMinSamples = 5

// realized reads the saving off a concluded randomized comparison. Harm
// is a negative saving and is reported as one.
func realized(k KindSavings) (perTip, total measurement.Value) {
	if !k.Verdict.Conclusive() {
		why := k.Verdict.Caveat
		switch {
		case k.Holdout.Count == 0:
			why = "no intervention of this kind was held back, so there is nothing to compare against"
		case k.Comparison.Samples < holdoutMinSamples:
			why = fmt.Sprintf("%d shown and %d held back so far; each side needs at least %d",
				k.Treated.Count, k.Holdout.Count, holdoutMinSamples)
		}
		u := measurement.Unknown("not established: " + why)
		return u, u
	}
	delta, ok := k.Verdict.Observed.Amount()
	if !ok {
		u := measurement.Unknown("not established: the difference was not measured")
		return u, u
	}
	src := fmt.Sprintf("randomized holdout, %d vs %d", k.Treated.Count, k.Holdout.Count)
	return measurement.Measured(delta, src), measurement.Measured(delta*float64(k.Treated.Count), src)
}
