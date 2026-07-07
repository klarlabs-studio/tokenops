package pricing

import (
	"fmt"
	"sort"
)

// ChangeKind classifies a Change.
type ChangeKind string

const (
	// ChangeAdded is a model present in the new snapshot but not the old.
	ChangeAdded ChangeKind = "added"
	// ChangeRemoved is a model present in the old snapshot but not the new.
	ChangeRemoved ChangeKind = "removed"
	// ChangeModified is a model present in both whose rate fields differ.
	ChangeModified ChangeKind = "modified"
)

// FieldDelta is a single rate field that moved between snapshots.
type FieldDelta struct {
	Field    string  `json:"field"` // "input" | "output" | "cache_read"
	Old      float64 `json:"old"`
	New      float64 `json:"new"`
	PctDelta float64 `json:"pct_delta"` // (new-old)/old*100; 0 when old is 0
}

// Change is one model's difference between two snapshots. For added/removed
// models Fields is empty; for modified models it lists only the fields that
// actually moved.
type Change struct {
	Model  string       `json:"model"`
	Kind   ChangeKind   `json:"kind"`
	Fields []FieldDelta `json:"fields,omitempty"`
}

// Diff compares old → new and returns the per-model changes, sorted by model.
// A model whose rates are identical produces no Change (so a no-op refresh
// prints nothing). This is the loud-drift mechanism from ADR 0002: the Opus
// error would have appeared here as
// `claude-opus-4-8 cache_read 0.50 → 1.50 (+200%)`.
func Diff(old, new Snapshot) []Change {
	seen := make(map[string]bool)
	var out []Change

	for model, nr := range new.Rates {
		seen[model] = true
		or, ok := old.Rates[model]
		if !ok {
			out = append(out, Change{Model: model, Kind: ChangeAdded})
			continue
		}
		if fields := fieldDeltas(or, nr); len(fields) > 0 {
			out = append(out, Change{Model: model, Kind: ChangeModified, Fields: fields})
		}
	}
	for model := range old.Rates {
		if !seen[model] {
			out = append(out, Change{Model: model, Kind: ChangeRemoved})
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}

// fieldDeltas returns the rate fields that differ between old and new.
func fieldDeltas(old, new Rate) []FieldDelta {
	var d []FieldDelta
	add := func(field string, o, n float64) {
		if o == n {
			return
		}
		d = append(d, FieldDelta{Field: field, Old: o, New: n, PctDelta: pctDelta(o, n)})
	}
	add("input", old.InputPerMillion, new.InputPerMillion)
	add("output", old.OutputPerMillion, new.OutputPerMillion)
	add("cache_read", old.CachedInputPerMillion, new.CachedInputPerMillion)
	return d
}

// pctDelta computes (new-old)/old*100, guarding division by zero. When old is
// zero and new is non-zero the change is unbounded, reported as +Inf-free by
// returning 0 (the caller renders the raw old→new which already conveys "new").
func pctDelta(old, new float64) float64 {
	if old == 0 {
		return 0
	}
	return (new - old) / old * 100
}

// FormatChange renders a Change as a human-readable line, e.g.
//
//	~ claude-opus-4-8 cache_read 0.50 → 1.50 (+200%)
//	+ claude-new-model (added)
//	- claude-old-model (removed)
//
// Multiple field deltas on one model are joined on one line.
func FormatChange(c Change) string {
	switch c.Kind {
	case ChangeAdded:
		return "+ " + c.Model + " (added)"
	case ChangeRemoved:
		return "- " + c.Model + " (removed)"
	default:
		parts := make([]string, 0, len(c.Fields))
		for _, f := range c.Fields {
			parts = append(parts, formatFieldDelta(f))
		}
		return "~ " + c.Model + " " + joinComma(parts)
	}
}

func formatFieldDelta(f FieldDelta) string {
	if f.Old == 0 {
		return fmt.Sprintf("%s %.4g → %.4g (new)", f.Field, f.Old, f.New)
	}
	return fmt.Sprintf("%s %.4g → %.4g (%+.0f%%)", f.Field, f.Old, f.New, f.PctDelta)
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
