package agentdx

import (
	"strings"
	"testing"
)

// Sessions run side by side. Each instruction is placed by its own
// session's context, and a turn counts toward its own session's open
// instruction: carried across, a subagent at 50k ran "at 900k" because
// the main session had just been there.
func TestContextCurveKeepsSessionsApart(t *testing.T) {
	recs := []Record{
		{At: at(0), SessionID: "main", Kind: KindAssistantTurn, InputTokens: 900_000, RequestID: "m0"},
		{At: at(1), SessionID: "sub", Kind: KindAssistantTurn, InputTokens: 50_000, RequestID: "s0"},
		{At: at(2), SessionID: "main", Kind: KindPrompt},
		{At: at(3), SessionID: "sub", Kind: KindPrompt},
		// Three turns of the main session while the subagent's
		// instruction is open, then one of its own.
		{At: at(4), SessionID: "main", Kind: KindAssistantTurn, InputTokens: 900_000, RequestID: "m1"},
		{At: at(5), SessionID: "main", Kind: KindAssistantTurn, InputTokens: 900_000, RequestID: "m2"},
		{At: at(6), SessionID: "main", Kind: KindAssistantTurn, InputTokens: 900_000, RequestID: "m3"},
		{At: at(7), SessionID: "sub", Kind: KindAssistantTurn, InputTokens: 50_000, RequestID: "s1"},
	}
	byLabel := map[string]ContextBand{}
	for _, b := range ComputeContextCurve(recs) {
		byLabel[b.Label] = b
	}
	if b := byLabel["<200k"]; b.Prompts != 1 || b.MedianTurns != 1 {
		t.Errorf("the subagent's band = %+v, want its one instruction with its one turn", b)
	}
	if b := byLabel[">800k"]; b.Prompts != 1 || b.MedianTurns != 3 {
		t.Errorf("the main session's band = %+v, want its one instruction with its three turns", b)
	}
}

// The note compares the halves only on enough calls, and names drift only
// when the rise is both material and more than chance.
func TestDegradationNoteNeedsEvidence(t *testing.T) {
	halves := func(loCalls, loRepeats, hiCalls, hiRepeats int) []ContextBand {
		return []ContextBand{
			{Lo: 0, Hi: 200_000, TotalCalls: loCalls, RepeatCalls: loRepeats},
			{Lo: 800_000, TotalCalls: hiCalls, RepeatCalls: hiRepeats},
		}
	}
	for _, tc := range []struct {
		name  string
		bands []ContextBand
		want  string
	}{
		// One repeat in two calls above the split used to read as drift.
		{"thin", halves(5_000, 100, 2, 1), "Too few calls"},
		// 50% more, over 600 calls a side: 6 against 9 repeats.
		{"material, chance", halves(600, 6, 600, 9), "No clear drift"},
		{"material, real", halves(5_000, 100, 5_000, 200), "Past 600k"},
		// Real at this size, but under the material 25%.
		{"real, small", halves(200_000, 4_000, 200_000, 4_600), "No clear drift"},
	} {
		if got := DegradationNote(tc.bands); !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
