package sqlite

import (
	"context"
	"fmt"
	"maps"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// CountBySource counts each source's events in the window, unlabelled
// ones (no source or an empty one) under "(none)", and leaves out a
// source with none in it: what grouping the window's rows answered.
func TestCountBySourceCountsEachSourceInTheWindow(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	base := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	add := func(i int, source string, at time.Time) {
		t.Helper()
		env := mustPromptEnvelope(t, fmt.Sprintf("e%d", i), at, &eventschema.PromptEvent{})
		env.Source = source
		if err := s.Append(ctx, env); err != nil {
			t.Fatal(err)
		}
	}
	i := 0
	for _, e := range []struct {
		source string
		hour   int
		n      int
	}{
		{"codex-jsonl", 1, 3}, {"claude-code-jsonl", 2, 2}, {"", 3, 1},
		{"old-only", -48, 2}, {"codex-jsonl", -48, 5}, {"late", 30, 1},
	} {
		for range e.n {
			add(i, e.source, base.Add(time.Duration(e.hour)*time.Hour))
			i++
		}
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE events SET source = NULL WHERE source = ''`); err != nil {
		t.Fatal(err)
	}
	add(i, "", base.Add(4*time.Hour))

	got, err := s.CountBySource(ctx, base, base.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"codex-jsonl": 3, "claude-code-jsonl": 2, "(none)": 2}
	if !maps.Equal(got, want) {
		t.Errorf("window counts = %v, want %v", got, want)
	}
	all, err := s.CountBySource(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	wantAll := map[string]int64{"codex-jsonl": 8, "claude-code-jsonl": 2, "(none)": 2, "old-only": 2, "late": 1}
	if !maps.Equal(all, wantAll) {
		t.Errorf("all counts = %v, want %v", all, wantAll)
	}
}
