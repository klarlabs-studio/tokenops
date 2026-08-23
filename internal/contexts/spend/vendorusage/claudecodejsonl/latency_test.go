package claudecodejsonl

import (
	"strings"
	"testing"
	"time"
)

func turnsFrom(t *testing.T, lines ...string) []Turn {
	t.Helper()
	var got []Turn
	if err := readReader(strings.NewReader(strings.Join(lines, "\n")), "proj", func(tn Turn) error {
		got = append(got, tn)
		return nil
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
	return got
}

// FVT medians PromptEvent.Latency, which no passive reader ever populated —
// so the median of an always-zero field graded a perfect A for absent data.
// The transcript carries the timing all along: an assistant turn's latency
// is the gap from the entry that preceded it.
func TestLatencyDerivedFromPrecedingEntry(t *testing.T) {
	got := turnsFrom(t,
		`{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:12Z","sessionId":"s","message":{"id":"a1","model":"m","usage":{"output_tokens":5}}}`,
	)
	if len(got) != 1 {
		t.Fatalf("turns = %d", len(got))
	}
	if got[0].Latency != 12*time.Second {
		t.Errorf("Latency = %v, want 12s", got[0].Latency)
	}
}

// Consecutive assistant turns are timed against each other, so a tool loop
// reports per-turn latency rather than one cumulative figure.
func TestLatencyBetweenConsecutiveTurns(t *testing.T) {
	got := turnsFrom(t,
		`{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:05Z","sessionId":"s","message":{"id":"a1","model":"m","usage":{"output_tokens":5}}}`,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:08Z","sessionId":"s","message":{"id":"a2","model":"m","usage":{"output_tokens":5}}}`,
	)
	if len(got) != 2 {
		t.Fatalf("turns = %d", len(got))
	}
	if got[1].Latency != 3*time.Second {
		t.Errorf("second turn Latency = %v, want 3s", got[1].Latency)
	}
}

// A gap spanning an operator's coffee break is not response time. Recording
// it would inflate FVT with idle minutes and quietly make the metric wrong
// in the other direction — better to report nothing for that turn.
func TestImplausibleGapIsNotRecorded(t *testing.T) {
	got := turnsFrom(t,
		`{"type":"user","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-08-23T11:30:00Z","sessionId":"s","message":{"id":"a1","model":"m","usage":{"output_tokens":5}}}`,
	)
	if len(got) != 1 {
		t.Fatalf("turns = %d", len(got))
	}
	if got[0].Latency != 0 {
		t.Errorf("Latency = %v, want 0 for a 90-minute gap (operator was away)", got[0].Latency)
	}
}

// The first entry in a file has nothing to measure against.
func TestFirstEntryHasNoLatency(t *testing.T) {
	got := turnsFrom(t,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"id":"a1","model":"m","usage":{"output_tokens":5}}}`,
	)
	if len(got) != 1 || got[0].Latency != 0 {
		t.Errorf("first entry should carry no latency: %+v", got)
	}
}

// Clock skew or out-of-order writes must not produce negative durations.
func TestOutOfOrderTimestampsYieldNoLatency(t *testing.T) {
	got := turnsFrom(t,
		`{"type":"user","timestamp":"2026-08-23T10:00:30Z","sessionId":"s","message":{"content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-08-23T10:00:00Z","sessionId":"s","message":{"id":"a1","model":"m","usage":{"output_tokens":5}}}`,
	)
	if len(got) != 1 || got[0].Latency != 0 {
		t.Errorf("backwards gap should yield no latency: %+v", got)
	}
}
