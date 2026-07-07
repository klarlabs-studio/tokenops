package pricing

import (
	"fmt"
	"sort"
)

// Consistency-guard tolerances and expected ratios. These encode the
// per-family invariant that caught the Opus ⅓ error: on the Anthropic list
// card, cache-read is 10% of input and output is 5× input. A row that
// violates either ratio beyond the tolerance is flagged for a human to eyeball
// before the snapshot is trusted — "consistency is not correctness", so this
// defends against the *source itself* being wrong.
const (
	// expectedOutputRatio is output ÷ input for the family.
	expectedOutputRatio = 5.0
	// expectedCacheRatio is cache-read ÷ input for the family.
	expectedCacheRatio = 0.10
	// outputTolerance is the fractional slack on expectedOutputRatio (±40%
	// of the ratio) — wide enough to not flag legitimate variation (some
	// families run 4× or 6×), tight enough to catch a 3× transcription error.
	outputTolerance = 0.40
	// cacheTolerance is the fractional slack on expectedCacheRatio (±50%).
	// Cache-read pricing varies more across models, so this is looser; it
	// still catches an order-of-magnitude mistake.
	cacheTolerance = 0.50
)

// Anomaly is one consistency-guard finding: a model whose rates violate a
// family ratio. It carries the expected and observed values so the message is
// actionable rather than a bare "looks wrong".
type Anomaly struct {
	Model    string  `json:"model"`
	Field    string  `json:"field"` // "output" | "cache_read"
	Input    float64 `json:"input"`
	Got      float64 `json:"got"`
	Expected float64 `json:"expected"`
	Message  string  `json:"message"`
}

// String renders an anomaly as a single human-readable line.
func (a Anomaly) String() string { return a.Message }

// Check runs the consistency guard over every rate in s and returns the
// anomalies, sorted by model then field for deterministic output. Entries
// with a zero input rate, or a zero value in the field being checked, are
// skipped — a missing number is not a wrong number, and cache-read is
// legitimately zero for models without prompt caching.
//
// This is the exact check that would have shouted about Opus being entered at
// $5/$25/$0.50 instead of $15/$75/$1.50: with input at $5 the guard expects
// output ≈ $25 (it was, so output alone stays quiet) but expects cache-read
// ≈ $0.50 — which matched the *wrong* input, so per-row consistency hid it.
// Checked against a corrected snapshot, or diffed against the source, the
// mismatch surfaces. The guard's real teeth are catching a row whose ratios
// are internally inconsistent (e.g. output not ≈5× input).
func Check(s Snapshot) []Anomaly {
	var out []Anomaly
	for _, model := range s.Models() {
		r := s.Rates[model]
		if r.InputPerMillion <= 0 {
			continue // no basis to check ratios against
		}
		if r.OutputPerMillion > 0 {
			ratio := r.OutputPerMillion / r.InputPerMillion
			if !withinTolerance(ratio, expectedOutputRatio, outputTolerance) {
				expected := r.InputPerMillion * expectedOutputRatio
				out = append(out, Anomaly{
					Model:    model,
					Field:    "output",
					Input:    r.InputPerMillion,
					Got:      r.OutputPerMillion,
					Expected: expected,
					Message: fmt.Sprintf(
						"%s output %.4g is %.1f× input (%.4g); expected ≈%.0f× (≈%.4g)",
						model, r.OutputPerMillion, ratio, r.InputPerMillion, expectedOutputRatio, expected),
				})
			}
		}
		if r.CachedInputPerMillion > 0 {
			ratio := r.CachedInputPerMillion / r.InputPerMillion
			if !withinTolerance(ratio, expectedCacheRatio, cacheTolerance) {
				expected := r.InputPerMillion * expectedCacheRatio
				out = append(out, Anomaly{
					Model:    model,
					Field:    "cache_read",
					Input:    r.InputPerMillion,
					Got:      r.CachedInputPerMillion,
					Expected: expected,
					Message: fmt.Sprintf(
						"%s cache_read %.4g is %.0f%% of input (%.4g); expected ≈%.0f%% (≈%.4g)",
						model, r.CachedInputPerMillion, ratio*100, r.InputPerMillion, expectedCacheRatio*100, expected),
				})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].Field < out[j].Field
	})
	return out
}

// withinTolerance reports whether got is within frac of want (relative to
// want). frac is a fraction of want, e.g. want=5, frac=0.4 accepts [3, 7].
func withinTolerance(got, want, frac float64) bool {
	lo := want * (1 - frac)
	hi := want * (1 + frac)
	return got >= lo && got <= hi
}
