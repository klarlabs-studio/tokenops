package agentdx

import (
	"fmt"
	"testing"
)

func call(seq int, ctx int64, tool, sig string) []Record {
	return []Record{
		{At: at(seq * 2), SessionID: "s", Kind: KindAssistantTurn, InputTokens: ctx},
		{At: at(seq*2 + 1), SessionID: "s", Kind: KindToolUse, ToolName: tool, CallSignature: sig},
	}
}

// The agent repeating a call it already made with identical arguments is
// it losing track of what it has already done. That is the drift an
// operator notices as "quality getting worse" and which a rejection rate
// never catches — they do not tell the agent off for it, they just watch
// it redo work.
func TestRepeatRateRisesWithContext(t *testing.T) {
	recs := make([]Record, 0, 400)
	seq := 0
	for range 100 { // clean band: every call distinct
		recs = append(recs, call(seq, 300_000, "Bash", fmt.Sprintf("cmd-%d", seq))...)
		seq++
	}
	for range 100 { // degraded band: the same call over and over
		recs = append(recs, call(seq, 900_000, "Bash", "same-command")...)
		seq++
	}
	bands := ComputeContextCurve(recs)
	var clean, degraded *ContextBand
	for i := range bands {
		switch {
		case bands[i].Contains(300_000):
			clean = &bands[i]
		case bands[i].Contains(900_000):
			degraded = &bands[i]
		}
	}
	if clean == nil || degraded == nil {
		t.Fatalf("expected both bands: %+v", bands)
	}
	if degraded.RepeatCallRatePct <= clean.RepeatCallRatePct {
		t.Errorf("repeat rate %.1f%% at 900k should exceed %.1f%% at 300k",
			degraded.RepeatCallRatePct, clean.RepeatCallRatePct)
	}
}

// The lookback is bounded so a long session cannot inflate the rate just
// by having more history to collide with. Without that control the metric
// measures session length, not drift.
func TestRepeatLookbackIsBounded(t *testing.T) {
	recs := make([]Record, 0, (repeatLookback+2)*2)
	seq := 0
	recs = append(recs, call(seq, 300_000, "Bash", "first")...)
	seq++
	for range repeatLookback + 1 { // push "first" out of the window
		recs = append(recs, call(seq, 300_000, "Bash", fmt.Sprintf("filler-%d", seq))...)
		seq++
	}
	recs = append(recs, call(seq, 300_000, "Bash", "first")...) // far-away echo

	bands := ComputeContextCurve(recs)
	if len(bands) == 0 {
		t.Fatal("no bands")
	}
	// The repeat of "first" is beyond the lookback, so it must not count.
	if bands[0].RepeatCalls != 0 {
		t.Errorf("RepeatCalls = %d, want 0 — a call outside the lookback is not a repeat",
			bands[0].RepeatCalls)
	}
}

// No signature, no signal: a client that does not record tool arguments
// cannot be judged on repeats, and guessing would be worse than silence.
func TestNoSignatureNoRepeatRate(t *testing.T) {
	recs := make([]Record, 0, 40)
	for i := range 20 {
		recs = append(recs, call(i, 300_000, "Bash", "")...)
	}
	bands := ComputeContextCurve(recs)
	if len(bands) > 0 && bands[0].RepeatCallRatePct != 0 {
		t.Errorf("RepeatCallRatePct = %.1f, want 0 without signatures", bands[0].RepeatCallRatePct)
	}
}

// An agent cannot repeat itself across a session boundary it has no
// memory of. Sharing one lookback across sessions counts two operators
// running the same command as the agent forgetting — enough, on real
// transcripts, to invert the trend entirely.
func TestRepeatsDoNotCrossSessions(t *testing.T) {
	recs := []Record{
		{At: at(0), SessionID: "a", Kind: KindAssistantTurn, InputTokens: 300_000},
		{At: at(1), SessionID: "a", Kind: KindToolUse, ToolName: "Bash", CallSignature: "go test"},
		{At: at(2), SessionID: "b", Kind: KindAssistantTurn, InputTokens: 300_000},
		{At: at(3), SessionID: "b", Kind: KindToolUse, ToolName: "Bash", CallSignature: "go test"},
	}
	bands := ComputeContextCurve(recs)
	if len(bands) == 0 {
		t.Fatal("no bands")
	}
	if bands[0].RepeatCalls != 0 {
		t.Errorf("RepeatCalls = %d, want 0 — the same command in two sessions is not a repeat",
			bands[0].RepeatCalls)
	}
}
