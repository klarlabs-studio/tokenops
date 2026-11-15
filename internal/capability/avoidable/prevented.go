package avoidable

import (
	"context"
	"fmt"
	"slices"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/explain"
	"go.klarlabs.de/tokenops/internal/capability/uniteconomics"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Prevented is what TokenOps kept from being spent in the period, by
// mechanism. It is the other half of the waste report: found is what the
// classes still show as avoidable, prevented is what the mechanisms
// already stopped. The two are never added together; each carries its
// own basis, and a mechanism that cannot be priced says why.
type Prevented struct {
	Mechanisms []Mechanism `json:"mechanisms"`
	// USD is the priced mechanisms added together. A mechanism whose
	// dollars are unknown is left out of the sum, not counted as zero.
	USD float64 `json:"usd"`
}

// Mechanism keys, in the order surfaces list them.
const (
	// MechReadGuard is the re-reads the read guard refused.
	MechReadGuard = "read_guard"
	// MechFmt is the output `tokenops fmt` compressed.
	MechFmt = "fmt"
	// MechAutopilot is the model moves the coach made that the randomized
	// holdout measured as saving money.
	MechAutopilot = "autopilot"
	// MechBoundary is the calls a boundary paused or the operator denied.
	MechBoundary = "boundary"
	// MechLoopBreaker is the calls the loop breaker stopped.
	MechLoopBreaker = "loop_breaker"
	// MechCompaction is the context the compaction line kept out, once
	// the developer's own weeks show the setting realized a saving (ADR
	// 0021).
	MechCompaction = "compaction"
	// MechLockout is the plan-window lockouts a pause or route avoided: a
	// counterfactual estimate, priced in blocked minutes.
	MechLockout = "lockout"
)

// Mechanism is one way TokenOps prevented spend.
type Mechanism struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Count is what the mechanism stopped or moved; Tokens the size of
	// what it kept out of the context or the output, when known.
	Count  int   `json:"count"`
	Tokens int64 `json:"tokens,omitempty"`
	// USD is the dollars prevented. Known only when the mechanism can be
	// priced; otherwise its Caveat says why not.
	USD measurement.Value `json:"usd"`
	// Basis says how the figures were made, in one line.
	Basis string `json:"basis"`
	// BlockedMinutes is the time a window would have been used up before
	// its reset, for the lockouts avoided.
	BlockedMinutes float64 `json:"blocked_minutes,omitempty"`
	// Counterfactual marks a figure for what would have happened without
	// the mechanism, not something observed.
	Counterfactual bool `json:"counterfactual,omitempty"`
}

// FmtSaving is one `tokenops fmt` compression recorded as an optimization
// event: the tokens it estimated it kept out of the output.
type FmtSaving struct {
	At     time.Time
	Tokens int64
}

// Hold is one call a boundary stopped: paused until approved, denied, or
// stopped by the loop breaker.
type Hold struct {
	At        time.Time
	Mechanism string
	// Denied is true when the operator refused the call rather than the
	// boundary pausing it.
	Denied bool
	// Forecasts are the plan windows as the pause or route read them.
	Forecasts []eventschema.WindowForecast
}

// Priced names the basis of the fmt and read-guard dollars.
const (
	basisFmt  = "tokens fmt estimated it kept out of the output, at the period's average API price per token"
	basisRef  = "re-reads refused by the read guard, each sized at its session's API price per token"
	basisAut  = "the randomized holdout's difference in spend after the routed work, with its interval when the comparison concluded"
	basisBnd  = "calls a boundary held until approved; not priced, since a held call never reached the model and its cost is not measured"
	basisLoop = "calls the loop breaker stopped after the same call repeated; not priced, since the repeated call's cost is not measured"
	basisLock = "a counterfactual estimate: a window counts once when, at the pace recorded before the first pause or route in it, " +
		"it would have run out before its reset; the minutes are those it would have stayed used up; not priced in dollars"
	basisCmp = "the weekly drop in context carried above the compaction line, four weeks after the setting against four before, " +
		"counted only when it and the reject rate fell beyond their usual week-to-week movement; at the period's average API price per token"
)

// buildPrevented sums the period's prevented spend by mechanism. It reads
// the report's usage and realized figures, so it runs after them.
func buildPrevented(in Inputs, r Report) Prevented {
	var out Prevented
	var rate float64
	if r.Usage.Tokens > 0 {
		rate = r.Usage.APIEquivalentUSD / float64(r.Usage.Tokens)
	}

	ref := Mechanism{Key: MechReadGuard, Name: "re-reads refused", Basis: basisRef,
		Count: r.Realized.RefusedRereads, Tokens: r.Realized.RefusedTokens,
		USD: measurement.Estimated(r.Realized.RefusedUSD, "read guard ledger", basisRef)}

	var fmtTokens int64
	var fmtCount int
	for _, f := range in.Fmt {
		if f.At.Before(in.Since) || !f.At.Before(in.Until) || f.Tokens <= 0 {
			continue
		}
		fmtCount++
		fmtTokens += f.Tokens
	}
	fmtUSD := measurement.Unknown("no API price is known for the period's usage")
	if rate > 0 {
		fmtUSD = measurement.Estimated(float64(fmtTokens)*rate, "fmt optimization events", basisFmt)
	}
	fm := Mechanism{Key: MechFmt, Name: "output compressed by fmt", Basis: basisFmt,
		Count: fmtCount, Tokens: fmtTokens, USD: fmtUSD}

	aut := Mechanism{Key: MechAutopilot, Name: "model moves verified by the holdout", Basis: basisAut,
		USD: measurement.Unknown("no holdout comparison has concluded yet")}
	var verified []measurement.Value
	for _, k := range in.Savings.Kinds {
		if k.Capability != learning.CapabilityRouting {
			continue
		}
		aut.Count += k.Treated.Count
		if k.RealizedUSD.Known() {
			verified = append(verified, k.RealizedUSD)
		}
	}
	if len(verified) > 0 {
		aut.USD = measurement.Sum(verified...)
	}

	var bnd, loop int
	for _, h := range in.Holds {
		if h.At.Before(in.Since) || !h.At.Before(in.Until) {
			continue
		}
		switch h.Mechanism {
		case MechLoopBreaker:
			loop++
		case MechBoundary:
			bnd++
		}
	}
	bm := Mechanism{Key: MechBoundary, Name: "calls a boundary held", Count: bnd, Basis: basisBnd,
		USD: measurement.Unknown(basisBnd)}
	lm := Mechanism{Key: MechLoopBreaker, Name: "calls the loop breaker stopped", Count: loop, Basis: basisLoop,
		USD: measurement.Unknown(basisLoop)}

	out.Mechanisms = []Mechanism{ref, fm, aut, bm, lm, compactionPrevented(in, rate), lockoutsAvoided(in)}
	for _, m := range out.Mechanisms {
		if v, ok := m.USD.Amount(); ok {
			out.USD += v
		}
	}
	return out
}

// holdOf classifies one boundary decision as a hold: a call paused until
// approved, denied by the operator, or stopped by the loop breaker. An
// approved call, and a shadow record (which would have held and did not),
// is not a hold.
func holdOf(env *eventschema.Envelope) (Hold, bool) {
	d, ok := env.Payload.(*eventschema.DecisionEvent)
	if !ok || d.Kind != "boundary" {
		return Hold{}, false
	}
	h := Hold{At: env.Timestamp, Forecasts: d.Forecasts}
	switch {
	case d.Stage == eventschema.DecisionStageRejected:
		h.Denied = true
	case d.Stage == eventschema.DecisionStageApplied && d.Approval.Approver == "":
	default:
		return Hold{}, false
	}
	h.Mechanism = MechBoundary
	if d.Policy == explain.BoundaryPolicyPrefix+config.LoopBreakerName {
		h.Mechanism = MechLoopBreaker
	}
	return h, true
}

// fmtSavingsOf reads the command_fmt optimization events.
func fmtSavingsOf(envs []*eventschema.Envelope) []FmtSaving {
	var out []FmtSaving
	for _, env := range envs {
		o, ok := env.Payload.(*eventschema.OptimizationEvent)
		if !ok || o.Kind != eventschema.OptimizationTypeCommandFmt {
			continue
		}
		out = append(out, FmtSaving{At: env.Timestamp, Tokens: o.EstimatedSavingsTokens})
	}
	return out
}

// ReadPrevented reads the fmt compressions and the boundary holds recorded
// in [since, until) from the event store.
func ReadPrevented(ctx context.Context, r uniteconomics.EventReader, since, until time.Time) ([]FmtSaving, []Hold, error) {
	var fmtEnvs []*eventschema.Envelope
	if err := r.EachMatching(ctx, sqlite.Filter{Type: eventschema.EventTypeOptimization, Since: since, Until: until},
		func(e *eventschema.Envelope) error {
			fmtEnvs = append(fmtEnvs, e)
			return nil
		}); err != nil {
		return nil, nil, fmt.Errorf("read optimization events: %w", err)
	}
	var holds []Hold
	if err := r.EachMatching(ctx, sqlite.Filter{Type: eventschema.EventTypeDecision, Source: "boundary", Since: since, Until: until},
		func(e *eventschema.Envelope) error {
			if h, ok := holdOf(e); ok {
				holds = append(holds, h)
			}
			return nil
		}); err != nil {
		return nil, nil, fmt.Errorf("read boundary decisions: %w", err)
	}
	return fmtSavingsOf(fmtEnvs), holds, nil
}

// compactionPrevented is the compaction setting's realized saving over the
// part of the period after the setting: the weekly drop in context carried
// above the line, scaled to that part. A setting not yet judged, or judged
// not realized, counts nothing and says why.
func compactionPrevented(in Inputs, rate float64) Mechanism {
	m := Mechanism{Key: MechCompaction, Name: "context kept under the compaction line", Basis: basisCmp}
	if in.Compaction == nil {
		m.USD = measurement.Unknown("tokenops has not set a compaction line")
		return m
	}
	v, from, ok := in.Compaction.Judge()
	if !ok {
		m.USD = measurement.Unknown("tokenops has not set a compaction line")
		return m
	}
	if !v.Realized {
		m.USD = measurement.Unknown("not yet shown: " + v.Reason)
		return m
	}
	start := in.Since
	if start.Before(from) {
		start = from
	}
	if !start.Before(in.Until) {
		m.USD = measurement.Unknown("the period ends before the weeks the setting is judged on")
		return m
	}
	m.Count = 1
	m.Tokens = int64(v.Oversized.Drop * in.Until.Sub(start).Hours() / (7 * 24))
	m.USD = measurement.Unknown("no API price is known for the period's usage")
	if rate > 0 {
		m.USD = measurement.Estimated(float64(m.Tokens)*rate, "compaction weekly rollup", basisCmp)
	}
	return m
}

// lockoutsAvoided counts the plan windows a pause or route kept from
// running out: at the pace recorded before the first one in the window, it
// would have been used up before its reset. Each window is counted once,
// at that first pace; one already used up was not avoided.
func lockoutsAvoided(in Inputs) Mechanism {
	m := Mechanism{Key: MechLockout, Name: "plan lockouts avoided", Basis: basisLock, Counterfactual: true,
		USD: measurement.Unknown(basisLock)}
	type window struct {
		provider, name string
		reset          time.Time
	}
	holds := slices.Clone(in.Holds)
	slices.SortStableFunc(holds, func(a, b Hold) int { return a.At.Compare(b.At) })
	seen := map[window]bool{}
	for _, h := range holds {
		if h.At.Before(in.Since) || !h.At.Before(in.Until) {
			continue
		}
		for _, f := range h.Forecasts {
			k := window{f.Provider, f.Window, f.ResetsAt.UTC()}
			if seen[k] {
				continue
			}
			seen[k] = true
			if f.RunsOutAt.IsZero() || !f.RunsOutAt.After(h.At) || !f.RunsOutAt.Before(f.ResetsAt) {
				continue
			}
			m.Count++
			m.BlockedMinutes += f.ResetsAt.Sub(f.RunsOutAt).Minutes()
		}
	}
	return m
}
