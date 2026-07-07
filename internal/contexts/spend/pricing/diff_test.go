package pricing

import (
	"strings"
	"testing"
	"time"
)

func diffSnap(rates map[string]Rate) Snapshot {
	return Snapshot{Source: "test", FetchedAt: time.Unix(0, 0), Rates: rates}
}

func TestDiff_AddedRemovedModified(t *testing.T) {
	old := diffSnap(map[string]Rate{
		"keep":    {InputPerMillion: 3, OutputPerMillion: 15, CachedInputPerMillion: 0.3},
		"changed": {InputPerMillion: 15, OutputPerMillion: 75, CachedInputPerMillion: 0.5},
		"gone":    {InputPerMillion: 1},
	})
	newer := diffSnap(map[string]Rate{
		"keep":    {InputPerMillion: 3, OutputPerMillion: 15, CachedInputPerMillion: 0.3},
		"changed": {InputPerMillion: 15, OutputPerMillion: 75, CachedInputPerMillion: 1.5},
		"brand":   {InputPerMillion: 2},
	})

	changes := Diff(old, newer)
	byModel := map[string]Change{}
	for _, c := range changes {
		byModel[c.Model] = c
	}
	if _, ok := byModel["keep"]; ok {
		t.Error("unchanged model should not appear in diff")
	}
	if byModel["brand"].Kind != ChangeAdded {
		t.Errorf("brand: %v, want added", byModel["brand"].Kind)
	}
	if byModel["gone"].Kind != ChangeRemoved {
		t.Errorf("gone: %v, want removed", byModel["gone"].Kind)
	}
	ch := byModel["changed"]
	if ch.Kind != ChangeModified || len(ch.Fields) != 1 || ch.Fields[0].Field != "cache_read" {
		t.Fatalf("changed: %+v, want single cache_read modification", ch)
	}
	if ch.Fields[0].PctDelta != 200 {
		t.Errorf("pct delta = %v, want +200", ch.Fields[0].PctDelta)
	}
}

func TestFormatChange_OpusStyleLine(t *testing.T) {
	c := Change{
		Model: "claude-opus-4-8", Kind: ChangeModified,
		Fields: []FieldDelta{{Field: "cache_read", Old: 0.5, New: 1.5, PctDelta: 200}},
	}
	got := FormatChange(c)
	// The exact drift the ADR wants to shout.
	if !strings.Contains(got, "claude-opus-4-8") || !strings.Contains(got, "cache_read") ||
		!strings.Contains(got, "0.5") || !strings.Contains(got, "1.5") || !strings.Contains(got, "+200%") {
		t.Errorf("FormatChange = %q, missing expected drift line parts", got)
	}
}

func TestFormatChange_AddedRemoved(t *testing.T) {
	if got := FormatChange(Change{Model: "m", Kind: ChangeAdded}); !strings.HasPrefix(got, "+ m") {
		t.Errorf("added format = %q", got)
	}
	if got := FormatChange(Change{Model: "m", Kind: ChangeRemoved}); !strings.HasPrefix(got, "- m") {
		t.Errorf("removed format = %q", got)
	}
}

func TestDiff_NoChangeEmpty(t *testing.T) {
	s := diffSnap(map[string]Rate{"m": {InputPerMillion: 3, OutputPerMillion: 15}})
	if got := Diff(s, s); len(got) != 0 {
		t.Errorf("identical snapshots should diff empty, got %v", got)
	}
}

func TestFieldDelta_NewFieldFromZero(t *testing.T) {
	old := diffSnap(map[string]Rate{"m": {InputPerMillion: 3, OutputPerMillion: 15}})
	newer := diffSnap(map[string]Rate{"m": {InputPerMillion: 3, OutputPerMillion: 15, CachedInputPerMillion: 0.3}})
	changes := Diff(old, newer)
	if len(changes) != 1 || len(changes[0].Fields) != 1 {
		t.Fatalf("want one cache_read field delta, got %+v", changes)
	}
	if !strings.Contains(FormatChange(changes[0]), "(new)") {
		t.Errorf("zero→value should render (new): %q", FormatChange(changes[0]))
	}
}
