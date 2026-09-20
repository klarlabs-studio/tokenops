package measurement_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/measurement"
)

// The invariant this package exists to hold: a value nobody measured
// must not read as zero. Every silent conversion TokenOps has shipped
// came from a float64 that had no way to say "I don't know".
func TestUnknownDoesNotReadAsZero(t *testing.T) {
	v := measurement.Unknown("no rate card for anthropic/claude-x")

	if _, ok := v.Amount(); ok {
		t.Error("an unknown value handed out an amount")
	}
	if v.Known() {
		t.Error("Known() is true for an unknown value")
	}
	// The caller that insists gets the fallback it named, never an
	// implicit 0.
	if got := v.AmountOr(-1); got != -1 {
		t.Errorf("AmountOr(-1) = %v, want the caller's fallback", got)
	}
}

// A measured zero is a real answer and must be distinguishable from
// no answer at all. Conflating them is the same bug in the other
// direction: "this cost nothing" reported as "we don't know".
func TestMeasuredZeroIsKnown(t *testing.T) {
	v := measurement.Measured(0, "sqlite_events")

	amount, ok := v.Amount()
	if !ok {
		t.Fatal("a measured zero was reported as unknown")
	}
	if amount != 0 {
		t.Errorf("amount = %v, want 0", amount)
	}
	if v.Quality() != measurement.QualityMeasured {
		t.Errorf("quality = %q, want measured", v.Quality())
	}
}

// Estimated must never present itself as measured. Downstream code
// decides whether to show a figure, warn, or refuse based on this.
func TestQualityIsCarriedNotInferred(t *testing.T) {
	cases := []struct {
		name string
		v    measurement.Value
		want measurement.Quality
	}{
		{"measured", measurement.Measured(1, "src"), measurement.QualityMeasured},
		{"derived", measurement.Derived(1, "src"), measurement.QualityDerived},
		{"estimated", measurement.Estimated(1, "src", "bytes/4 heuristic"), measurement.QualityEstimated},
		{"unknown", measurement.Unknown("why"), measurement.QualityUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.v.Quality(); got != tc.want {
				t.Errorf("quality = %q, want %q", got, tc.want)
			}
		})
	}
}

// An estimate must say what it estimated from. A figure an operator
// cannot interrogate is the thing this package replaces.
func TestEstimatedCarriesItsCaveat(t *testing.T) {
	v := measurement.Estimated(250, "tokenizer_absent", "bytes/4 heuristic")
	if !strings.Contains(v.Caveat(), "bytes/4") {
		t.Errorf("caveat lost: %q", v.Caveat())
	}
	if v.Source() != "tokenizer_absent" {
		t.Errorf("source = %q", v.Source())
	}
}

// Coverage is a separate axis from quality. A figure can be measured
// exactly and still account for only part of the population — which is
// precisely the analytics aggregator's case, where unpriced models are
// skipped and the total looks complete.
func TestCoverageIsSeparateFromQuality(t *testing.T) {
	v := measurement.Measured(42, "sqlite_events").
		Covering(90, 10, "unpriced_model:anthropic/claude-x")

	if v.Quality() != measurement.QualityMeasured {
		t.Errorf("covering changed the quality to %q", v.Quality())
	}
	if v.Complete() {
		t.Error("a value excluding 10 of 100 reported itself complete")
	}
	c := v.Coverage()
	if c.Included != 90 || c.Excluded != 10 {
		t.Errorf("coverage = %+v", c)
	}
	if len(c.Reasons) != 1 || !strings.Contains(c.Reasons[0], "claude-x") {
		t.Errorf("coverage reasons lost: %+v", c.Reasons)
	}
}

// Nothing excluded means complete. This is the common case and must not
// require the caller to say so.
func TestValueWithNothingExcludedIsComplete(t *testing.T) {
	if !measurement.Measured(1, "src").Complete() {
		t.Error("a value with no exclusions reported itself incomplete")
	}
}

// An unknown value is never complete, whatever its coverage says.
// "We excluded nothing" is not the same as "we know the answer".
func TestUnknownIsNeverComplete(t *testing.T) {
	if measurement.Unknown("why").Complete() {
		t.Error("an unknown value reported itself complete")
	}
}

// The serialised form is what reaches the dashboard, the MCP tools and
// `--json`. An unknown value must not marshal to a number there either:
// that is the same bug, one layer out.
func TestUnknownMarshalsWithoutANumber(t *testing.T) {
	body, err := json.Marshal(measurement.Unknown("no rate card"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, present := back["amount"]; present && v != nil {
		t.Errorf("unknown marshalled an amount: %s", body)
	}
	if back["quality"] != string(measurement.QualityUnknown) {
		t.Errorf("quality missing from %s", body)
	}
	if !strings.Contains(string(body), "no rate card") {
		t.Errorf("caveat missing from %s", body)
	}
}

// A known value round-trips, so a figure crossing the MCP boundary
// keeps its provenance instead of arriving as a bare float.
func TestValueRoundTripsThroughJSON(t *testing.T) {
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	orig := measurement.Measured(12.5, "sqlite_events").
		At(at).
		Covering(3, 1, "unpriced_model:x")

	body, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back measurement.Value
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	amount, ok := back.Amount()
	if !ok || amount != 12.5 {
		t.Errorf("amount = %v, ok = %v", amount, ok)
	}
	if back.Quality() != measurement.QualityMeasured || back.Source() != "sqlite_events" {
		t.Errorf("provenance lost: %+v", back)
	}
	if !back.ObservedAt().Equal(at) {
		t.Errorf("observed-at = %v, want %v", back.ObservedAt(), at)
	}
	if back.Complete() {
		t.Error("coverage lost in the round trip")
	}
}

// The zero Value is what a struct field starts as. It must be unknown
// rather than a measured zero, or every unset field silently becomes a
// confident answer.
func TestZeroValueIsUnknown(t *testing.T) {
	var v measurement.Value
	if v.Known() {
		t.Error("the zero Value claims to be known")
	}
	if v.Quality() != measurement.QualityUnknown {
		t.Errorf("zero Value quality = %q, want unknown", v.Quality())
	}
}

// Summing is where provenance usually dies: two values are added and
// the result claims the better of the two. The sum of a measured and an
// estimated figure is estimated, and the sum of anything with an
// unknown is unknown — that is the whole point.
func TestSumTakesTheWeakestQuality(t *testing.T) {
	cases := []struct {
		name string
		in   []measurement.Value
		want measurement.Quality
	}{
		{"all measured", []measurement.Value{
			measurement.Measured(1, "a"), measurement.Measured(2, "b"),
		}, measurement.QualityMeasured},
		{"one estimated", []measurement.Value{
			measurement.Measured(1, "a"), measurement.Estimated(2, "b", "guess"),
		}, measurement.QualityEstimated},
		{"one derived", []measurement.Value{
			measurement.Measured(1, "a"), measurement.Derived(2, "b"),
		}, measurement.QualityDerived},
		{"one unknown", []measurement.Value{
			measurement.Measured(1, "a"), measurement.Unknown("no card"),
		}, measurement.QualityUnknown},
		{"nothing at all", nil, measurement.QualityUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := measurement.Sum(tc.in...)
			if got.Quality() != tc.want {
				t.Errorf("quality = %q, want %q", got.Quality(), tc.want)
			}
		})
	}
}

// Summing adds the amounts and accumulates the exclusions, so a total
// says how much of itself it could not see.
func TestSumAddsAmountsAndCoverage(t *testing.T) {
	got := measurement.Sum(
		measurement.Measured(1.5, "a").Covering(10, 0),
		measurement.Measured(2.5, "b").Covering(5, 2, "unpriced_model:x"),
	)
	amount, ok := got.Amount()
	if !ok || amount != 4.0 {
		t.Errorf("amount = %v, ok = %v, want 4", amount, ok)
	}
	c := got.Coverage()
	if c.Included != 15 || c.Excluded != 2 {
		t.Errorf("coverage = %+v, want 15 included / 2 excluded", c)
	}
	if got.Complete() {
		t.Error("a sum hiding 2 exclusions reported itself complete")
	}
}

// A sum that includes an unknown must not hand out the partial total as
// though it were the answer.
func TestSumWithUnknownWithholdsTheAmount(t *testing.T) {
	got := measurement.Sum(
		measurement.Measured(1, "a"),
		measurement.Unknown("no rate card for x"),
	)
	if _, ok := got.Amount(); ok {
		t.Error("a sum containing an unknown handed out an amount")
	}
	if !strings.Contains(got.Caveat(), "no rate card") {
		t.Errorf("the unknown's reason was dropped: %q", got.Caveat())
	}
}
