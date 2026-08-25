package agentdx

import "testing"

// rejectAt builds one unit at a given context size. Each unit needs its
// own increasing timestamps: the curve walks records chronologically, so
// units sharing a clock would all see whichever context sorted last.
func rejectAt(seq int, ctx int64, reject bool) []Record {
	return []Record{
		{At: at(seq * 2), SessionID: "s", Kind: KindAssistantTurn, InputTokens: ctx},
		{At: at(seq*2 + 1), SessionID: "s", Kind: KindPrompt, Rejects: reject},
	}
}

// The operator's own rejection is the closest thing to a quality verdict
// in a transcript: they read what came back and said it was wrong. Tool
// errors and rework measure mechanical correctness, which is a different
// question.
func TestContextCurveTracksRejection(t *testing.T) {
	// A clean band and a degraded one; two records per unit.
	recs := make([]Record, 0, 400)
	seq := 0
	for range 100 {
		recs = append(recs, rejectAt(seq, 450_000, false)...)
		seq++
	}
	for i := range 100 {
		recs = append(recs, rejectAt(seq, 900_000, i < 20)...)
		seq++
	}
	bands := ComputeContextCurve(recs)
	if len(bands) < 2 {
		t.Fatalf("bands = %+v, want at least two", bands)
	}
	var clean, degraded *ContextBand
	for i := range bands {
		switch {
		case bands[i].Contains(450_000):
			clean = &bands[i]
		case bands[i].Contains(900_000):
			degraded = &bands[i]
		}
	}
	if clean == nil || degraded == nil {
		t.Fatalf("expected both bands populated: %+v", bands)
	}
	if degraded.RejectRatePct <= clean.RejectRatePct {
		t.Errorf("degraded band %.1f%% should exceed clean %.1f%%",
			degraded.RejectRatePct, clean.RejectRatePct)
	}
}

// The sweet spot is the band with the lowest rejection rate that has
// enough observations to mean anything — the operator's own measured
// best, not a threshold someone invented.
func TestSweetSpotIsTheOperatorsOwnMinimum(t *testing.T) {
	recs := make([]Record, 0, 800)
	seq := 0
	for i := range 200 {
		recs = append(recs, rejectAt(seq, 450_000, i < 4)...) // 2%
		seq++
	}
	for i := range 200 {
		recs = append(recs, rejectAt(seq, 900_000, i < 30)...) // 15%
		seq++
	}
	spot, ok := SweetSpot(ComputeContextCurve(recs))
	if !ok {
		t.Fatal("expected a sweet spot from two well-populated bands")
	}
	if !spot.Contains(450_000) {
		t.Errorf("sweet spot = %s, want the band holding 450k", spot.Label)
	}
}

// A band nobody exercised cannot be a sweet spot; a handful of prompts
// swings a percentage on single events.
func TestSweetSpotIgnoresThinBands(t *testing.T) {
	recs := make([]Record, 0, 402)
	recs = append(recs, rejectAt(0, 150_000, false)...) // one prompt, 0% — thin
	for i := range 200 {
		recs = append(recs, rejectAt(i+1, 450_000, i < 10)...)
	}
	spot, ok := SweetSpot(ComputeContextCurve(recs))
	if !ok {
		t.Fatal("expected a sweet spot")
	}
	if spot.Contains(150_000) {
		t.Errorf("a one-prompt band must not win: %s", spot.Label)
	}
}

// Nothing to measure, nothing claimed.
func TestNoCurveWithoutData(t *testing.T) {
	if bands := ComputeContextCurve(nil); len(bands) != 0 {
		t.Errorf("bands = %+v, want none", bands)
	}
	if _, ok := SweetSpot(nil); ok {
		t.Error("no bands must yield no sweet spot")
	}
}
