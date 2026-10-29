package taskruntime

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
)

// Each recorded turn goes to the task of its session that started last at
// or before it; a task no turn fell in keeps a null usage; a turn from
// before a session's first task, or of a session no task covers, is
// placed on none. Codex's transcript and turn readers name a session
// differently and still match.
func TestAttachUsagePlacesTurnsBySessionAndTime(t *testing.T) {
	t0 := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	codexFile := "2026-10-29T10-00-00-01a18908-d0b6-75e2-a23f-310762054326"
	codexUUID := "01a18908-d0b6-75e2-a23f-310762054326"
	tasks := []story.Task{
		{SessionID: "s1", Start: t0},
		{SessionID: "s1", Start: t0.Add(time.Hour)},
		{SessionID: "s2", Start: t0},
		{SessionID: codexFile, Start: t0},
	}
	analysed := make([]Task, len(tasks))
	turn := func(session string, at time.Duration, value float64, priced bool) Turn {
		return Turn{SessionID: session, At: t0.Add(at), InputTokens: 100, OutputTokens: 10,
			CacheReadTokens: 60, APIEquivalentUSD: value, Priced: priced}
	}
	unplaced := AttachUsage(tasks, analysed, []Turn{
		turn("s1", 5*time.Minute, 1, true),
		turn("s1", 59*time.Minute, 2, true),
		turn("s1", 61*time.Minute, 4, false),
		turn("s1", -time.Minute, 8, true),    // before the session's first task
		turn("other", time.Minute, 16, true), // a session no task covers
		turn(codexUUID, time.Minute, 0.5, true),
	})
	if unplaced != 2 {
		t.Errorf("unplaced = %d, want 2", unplaced)
	}
	first, second := analysed[0].Usage, analysed[1].Usage
	if first == nil || first.Turns != 2 || first.APIEquivalentUSD != 3 || first.InputTokens != 200 || first.CacheReadTokens != 120 {
		t.Errorf("first task usage = %+v", first)
	}
	if second == nil || second.Turns != 1 || second.APIEquivalentUSD != 4 || second.UnpricedTurns != 1 {
		t.Errorf("second task usage = %+v", second)
	}
	if analysed[2].Usage != nil {
		t.Errorf("a task no turn fell in has no usage, not zero: %+v", analysed[2].Usage)
	}
	if u := analysed[3].Usage; u == nil || u.APIEquivalentUSD != 0.5 {
		t.Errorf("the Codex task matches its turns by UUID: %+v", u)
	}
}

// The summary's cost figures are over the runtime samples with usage, and
// say how many understate.
func TestSummariseCostsOverTheSamplesWithUsage(t *testing.T) {
	secs := 60.0
	sample := func(u *Usage) Task { return Task{Status: StatusCompleted, ObservedSeconds: &secs, Usage: u} }
	g := Summarise([]Task{
		sample(&Usage{Turns: 1, APIEquivalentUSD: 1, InputTokens: 100, OutputTokens: 10}),
		sample(&Usage{Turns: 1, APIEquivalentUSD: 3, UnpricedTurns: 1}),
		sample(nil),
		{Status: StatusOpen, ObservedSeconds: &secs, Usage: &Usage{APIEquivalentUSD: 100}},
	})
	if g.UsageSamples != 2 || g.UsageUnpriced != 1 {
		t.Errorf("usage samples %d, unpriced %d", g.UsageSamples, g.UsageUnpriced)
	}
	if g.APIEquivalentUSD.N != 2 || *g.APIEquivalentUSD.Median != 1 || *g.APIEquivalentUSD.P90 != 3 {
		t.Errorf("value distribution %+v", g.APIEquivalentUSD)
	}
	if empty := Summarise([]Task{sample(nil)}); empty.APIEquivalentUSD.Median != nil {
		t.Errorf("no usage is a null median, never zero: %+v", empty.APIEquivalentUSD)
	}
}
