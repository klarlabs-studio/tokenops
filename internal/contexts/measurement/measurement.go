// Package measurement is the domain-neutral representation of a number
// TokenOps reports, together with how it was arrived at.
//
// It exists because every silent conversion this codebase has shipped came
// from a float64 with no way to say "I don't know". An unpriced model
// contributed 0 to a cost total; a proxy with no tokenizer emitted zero
// token counts; a failed estimate returned the same int64 a real
// measurement would. In each case the number was wrong in the direction
// that looks like good news, and nothing downstream could tell.
//
// The two invariants:
//
//   - unknown never becomes 0 — Amount withholds the number and makes
//     the caller decide, rather than handing out a zero that reads as an
//     answer;
//   - estimated never becomes measured — Quality travels with the value,
//     including through Sum, which takes the weakest quality of its
//     inputs rather than the best.
//
// Coverage is a third, separate axis: a figure can be measured exactly
// and still account for only part of the population. That is the
// analytics aggregator's case, where unpriced models are skipped and the
// total nevertheless presents itself as the whole.
//
// This package is deliberately free of any notion of what is being
// measured. Cost, tokens, latency and quality scores are all the same
// shape to it, which is what lets provenance cross context boundaries
// without each context inventing its own.
package measurement

import (
	"encoding/json"
	"strings"
	"time"
)

// Quality ranks how much trust a reported number carries, weakest last.
// The order matters: Sum reports the weakest quality among its inputs.
type Quality string

const (
	// QualityMeasured — observed directly from a record of what happened.
	QualityMeasured Quality = "measured"
	// QualityDerived — computed from measured inputs by a rule that does
	// not itself introduce uncertainty (a sum, a rate card lookup).
	QualityDerived Quality = "derived"
	// QualityEstimated — modelled rather than observed. The Caveat says
	// how.
	QualityEstimated Quality = "estimated"
	// QualityUnknown — not determined. The amount is meaningless and is
	// not handed out.
	QualityUnknown Quality = "unknown"
)

// rank orders qualities so Sum can take the weakest. Higher is weaker.
func (q Quality) rank() int {
	switch q {
	case QualityMeasured:
		return 0
	case QualityDerived:
		return 1
	case QualityEstimated:
		return 2
	default:
		return 3
	}
}

// Coverage says how much of the population a value accounts for.
//
// It is not a confidence score. A cost total can be measured exactly and
// still exclude every event whose model has no rate card; Coverage is how
// that total admits it.
type Coverage struct {
	// Included is the number of units (events, requests, records) the
	// amount accounts for.
	Included int64 `json:"included"`
	// Excluded is the number it does not. Non-zero means incomplete.
	Excluded int64 `json:"excluded,omitempty"`
	// Reasons names why, one entry per distinct cause. Kept short and
	// machine-greppable ("unpriced_model:anthropic/claude-x") so a
	// dashboard can group by cause.
	Reasons []string `json:"reasons,omitempty"`
}

// Value is a number reported together with its provenance.
//
// The fields are unexported on purpose. An exported float64 is exactly
// what allows an unknown to be read as zero, and no amount of
// documentation prevents it — Amount returns a bool the caller has to
// handle.
//
// The zero Value is unknown, which is what makes it safe as a struct
// field: an unset measurement does not silently become a confident zero.
type Value struct {
	amount     float64
	quality    Quality
	source     string
	caveat     string
	observedAt time.Time
	coverage   Coverage
}

// Measured records a number observed directly. source names where it was
// observed ("sqlite_events", "vendor_usage_api") so a caveat shown to an
// operator can point at something.
func Measured(amount float64, source string) Value {
	return Value{amount: amount, quality: QualityMeasured, source: source}
}

// Derived records a number computed from measured inputs by a rule that
// introduces no uncertainty of its own.
func Derived(amount float64, source string) Value {
	return Value{amount: amount, quality: QualityDerived, source: source}
}

// Estimated records a modelled number. The caveat must say what the model
// was — "bytes/4 heuristic", "extrapolated from 3 samples" — because an
// estimate an operator cannot interrogate is the thing this package
// replaces.
func Estimated(amount float64, source, caveat string) Value {
	return Value{amount: amount, quality: QualityEstimated, source: source, caveat: caveat}
}

// Unknown records that no number was determined. The reason is carried so
// the gap can be explained rather than merely reported.
func Unknown(reason string) Value {
	return Value{quality: QualityUnknown, caveat: reason}
}

// At stamps when the underlying observation was made. Returns a copy;
// Value is immutable.
func (v Value) At(t time.Time) Value {
	v.observedAt = t
	return v
}

// Covering records how much of the population the amount accounts for.
// Returns a copy.
func (v Value) Covering(included, excluded int64, reasons ...string) Value {
	v.coverage = Coverage{Included: included, Excluded: excluded, Reasons: reasons}
	return v
}

// WithCaveat attaches or replaces the explanation attached to a value.
// Returns a copy.
func (v Value) WithCaveat(caveat string) Value {
	v.caveat = caveat
	return v
}

// Amount returns the number and whether there is one. The bool is the
// whole point: a caller cannot reach the float without acknowledging that
// it may not exist.
func (v Value) Amount() (float64, bool) {
	if v.quality == QualityUnknown || v.quality == "" {
		return 0, false
	}
	return v.amount, true
}

// AmountOr returns the number, or the caller's own fallback when there is
// none. Use it where a default is a deliberate decision — never to spell
// AmountOr(0) as a way around Amount.
func (v Value) AmountOr(fallback float64) float64 {
	if amount, ok := v.Amount(); ok {
		return amount
	}
	return fallback
}

// Known reports whether the value carries a number at all.
func (v Value) Known() bool {
	_, ok := v.Amount()
	return ok
}

// Quality reports how the number was arrived at. The zero Value is
// unknown.
func (v Value) Quality() Quality {
	if v.quality == "" {
		return QualityUnknown
	}
	return v.quality
}

// Source names where the observation came from.
func (v Value) Source() string { return v.source }

// Caveat explains an estimate, or why an unknown is unknown.
func (v Value) Caveat() string { return v.caveat }

// ObservedAt is when the underlying observation was made. Zero when the
// caller did not say.
func (v Value) ObservedAt() time.Time { return v.observedAt }

// Coverage reports how much of the population the amount accounts for.
func (v Value) Coverage() Coverage { return v.coverage }

// Complete reports whether the value accounts for everything it was asked
// about. An unknown is never complete: excluding nothing is not the same
// as knowing the answer.
func (v Value) Complete() bool {
	return v.Known() && v.coverage.Excluded == 0
}

// Sum adds values and reports the weakest quality among them.
//
// This is where provenance usually dies. Adding a measured figure to an
// estimated one gives an estimate, and adding anything to an unknown
// gives an unknown — returning the partial total would be the silent
// conversion this package exists to prevent, one aggregation later.
//
// Coverage accumulates, so a total says how much of itself it could not
// see. Summing nothing is unknown, not zero: "no rows matched" and "the
// rows matched and cost nothing" are different answers.
func Sum(values ...Value) Value {
	if len(values) == 0 {
		return Unknown("nothing to sum")
	}

	out := Value{quality: QualityMeasured}
	var caveats []string
	var reasons []string
	seenReason := map[string]bool{}

	for _, v := range values {
		q := v.Quality()
		if q.rank() > out.quality.rank() {
			out.quality = q
		}
		if amount, ok := v.Amount(); ok {
			out.amount += amount
		}
		if v.caveat != "" {
			caveats = append(caveats, v.caveat)
		}
		if out.source == "" {
			out.source = v.source
		} else if v.source != "" && out.source != v.source {
			out.source = "mixed"
		}
		if v.observedAt.After(out.observedAt) {
			out.observedAt = v.observedAt
		}
		out.coverage.Included += v.coverage.Included
		out.coverage.Excluded += v.coverage.Excluded
		for _, r := range v.coverage.Reasons {
			if !seenReason[r] {
				seenReason[r] = true
				reasons = append(reasons, r)
			}
		}
	}

	out.coverage.Reasons = reasons
	out.caveat = joinCaveats(caveats)
	return out
}

func joinCaveats(caveats []string) string {
	unique := make([]string, 0, len(caveats))
	seen := make(map[string]struct{}, len(caveats))
	for _, caveat := range caveats {
		if _, ok := seen[caveat]; ok {
			continue
		}
		seen[caveat] = struct{}{}
		unique = append(unique, caveat)
	}
	return strings.Join(unique, "; ")
}

// wire is the serialised shape. Amount is a pointer so an unknown
// marshals without a number: emitting 0 here would reintroduce the bug
// one layer out, where the dashboard and the MCP tools read it.
type wire struct {
	Amount     *float64  `json:"amount,omitempty"`
	Quality    Quality   `json:"quality"`
	Source     string    `json:"source,omitempty"`
	Caveat     string    `json:"caveat,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitempty"`
	Coverage   *Coverage `json:"coverage,omitempty"`
}

// MarshalJSON emits the value with its provenance intact.
func (v Value) MarshalJSON() ([]byte, error) {
	w := wire{
		Quality:    v.Quality(),
		Source:     v.source,
		Caveat:     v.caveat,
		ObservedAt: v.observedAt,
	}
	if amount, ok := v.Amount(); ok {
		w.Amount = &amount
	}
	if v.coverage.Included != 0 || v.coverage.Excluded != 0 {
		c := v.coverage
		w.Coverage = &c
	}
	return json.Marshal(w)
}

// UnmarshalJSON restores a value, including the absence of an amount.
func (v *Value) UnmarshalJSON(b []byte) error {
	var w wire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*v = Value{
		quality:    w.Quality,
		source:     w.Source,
		caveat:     w.Caveat,
		observedAt: w.ObservedAt,
	}
	if w.Amount != nil {
		v.amount = *w.Amount
	}
	if w.Coverage != nil {
		v.coverage = *w.Coverage
	}
	return nil
}
