package agentdx

import (
	"math"
	"strings"
	"testing"
	"time"
)

// row is an effort row with enough instructions, priced.
func row(client, model, effort string, n int, firstTry, rejected, interrupted, usdPer float64) EffortRow {
	return EffortRow{
		Client: client, Model: model, Effort: effort, Instructions: n, Enough: n >= MinEffortInstructions,
		FirstTryPct: firstTry, RejectedPct: rejected, InterruptedPct: interrupted,
		USDPerInstruction: usdPer, APIEquivalentUSD: usdPer * float64(n),
	}
}

func TestSuggestEffort(t *testing.T) {
	const cc, model = "claude-code", "claude-opus-5-5"
	tests := []struct {
		name     string
		rows     []EffortRow
		wantTo   string // "" for no suggestion
		wantFrom string
	}{
		{
			name:     "a cheaper level that does as well is suggested",
			rows:     []EffortRow{row(cc, model, "medium", 40, 70, 5, 4, 0.40), row(cc, model, "xhigh", 120, 72, 6, 5, 1.20)},
			wantFrom: "xhigh", wantTo: "medium",
		},
		{
			name:     "outcomes within the margin still count as not worse",
			rows:     []EffortRow{row(cc, model, "medium", 40, 66, 9, 8, 0.40), row(cc, model, "xhigh", 120, 70, 5, 4, 1.20)},
			wantFrom: "xhigh", wantTo: "medium",
		},
		{
			name: "first-try success worse beyond the margin: none",
			rows: []EffortRow{row(cc, model, "medium", 40, 60, 5, 4, 0.40), row(cc, model, "xhigh", 120, 72, 5, 4, 1.20)},
		},
		{
			name: "rejected worse beyond the margin: none",
			rows: []EffortRow{row(cc, model, "medium", 40, 72, 12, 4, 0.40), row(cc, model, "xhigh", 120, 72, 5, 4, 1.20)},
		},
		{
			name: "interrupted worse beyond the margin: none",
			rows: []EffortRow{row(cc, model, "medium", 40, 72, 5, 11, 0.40), row(cc, model, "xhigh", 120, 72, 5, 4, 1.20)},
		},
		{
			name: "a saving below the material floor: none",
			rows: []EffortRow{row(cc, model, "high", 40, 72, 5, 4, 0.95), row(cc, model, "xhigh", 120, 72, 5, 4, 1.20)},
		},
		{
			name: "too few instructions at the lower level: none",
			rows: []EffortRow{row(cc, model, "medium", 29, 72, 5, 4, 0.40), row(cc, model, "xhigh", 120, 72, 5, 4, 1.20)},
		},
		{
			name: "too few at the dominant level: none",
			rows: []EffortRow{row(cc, model, "low", 25, 72, 5, 4, 0.40), row(cc, model, "high", 28, 72, 5, 4, 1.20)},
		},
		{
			name: "the dominant level is already the lowest: none",
			rows: []EffortRow{row(cc, model, "medium", 120, 72, 5, 4, 0.40), row(cc, model, "xhigh", 40, 72, 5, 4, 1.20)},
		},
		{
			name: "no cost source: none",
			rows: []EffortRow{row(cc, model, "medium", 40, 72, 5, 4, 0), row(cc, model, "xhigh", 120, 72, 5, 4, 0)},
		},
		{
			name: "levels of different models are never compared",
			rows: []EffortRow{row(cc, "claude-sonnet-5", "low", 40, 72, 5, 4, 0.10), row(cc, model, "xhigh", 120, 72, 5, 4, 1.20)},
		},
		{
			name: "the same model in another client is never compared",
			rows: []EffortRow{row("opencode", model, "low", 40, 72, 5, 4, 0.10), row(cc, model, "xhigh", 120, 72, 5, 4, 1.20)},
		},
		{
			name: "an unknown level is not ranked against the known ones",
			rows: []EffortRow{row(cc, model, "turbo", 40, 72, 5, 4, 0.10), row(cc, model, "xhigh", 120, 72, 5, 4, 1.20)},
		},
		{
			name: "of several qualifying levels the cheapest is suggested",
			rows: []EffortRow{
				row(cc, model, "low", 35, 70, 6, 4, 0.20), row(cc, model, "medium", 40, 72, 5, 4, 0.40),
				row(cc, model, "max", 120, 72, 5, 4, 1.60),
			},
			wantFrom: "max", wantTo: "low",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SuggestEffort(tt.rows, 7)
			if tt.wantTo == "" {
				if len(got) != 0 {
					t.Fatalf("got %+v, want no suggestion", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("got %d suggestions, want 1: %+v", len(got), got)
			}
			s := got[0]
			if s.From.Effort != tt.wantFrom || s.To.Effort != tt.wantTo || s.Model != model || s.Client != cc {
				t.Errorf("got %s %s %s → %s", s.Client, s.Model, s.From.Effort, s.To.Effort)
			}
		})
	}
}

// The saving is the developer's own volume at the current level, priced at
// the difference, scaled from the window to a month, and the suggestion
// says plainly that it is an estimate and an observation.
func TestSuggestEffortEstimatesTheMonthFromTheWindow(t *testing.T) {
	rows := []EffortRow{
		row("codex", "gpt-5.5", "medium", 40, 70, 5, 4, 0.50),
		row("codex", "gpt-5.5", "high", 70, 70, 5, 4, 1.50),
	}
	got := SuggestEffort(rows, 7)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	s := got[0]
	// 70 instructions a week × $1.00 less each × 30/7.
	if want := 70 * 1.0 * 30 / 7; math.Abs(s.MonthlySavingUSD-want) > 0.01 {
		t.Errorf("monthly saving = %.2f, want %.2f", s.MonthlySavingUSD, want)
	}
	if math.Abs(s.CostDropPct-66.7) > 0.05 {
		t.Errorf("cost drop = %.1f%%, want 66.7%%", s.CostDropPct)
	}
	for _, want := range []string{"harder work", "observation"} {
		if !strings.Contains(EffortConfound, want) {
			t.Errorf("confound %q does not say %q", EffortConfound, want)
		}
	}
	if !strings.Contains(s.Basis, "estimate") {
		t.Errorf("basis %q does not say it is an estimate", s.Basis)
	}
}

// A window of no days, or all history, cannot be scaled to a month: the
// suggestion stands, without a monthly figure.
func TestSuggestEffortWithoutAWindowHasNoMonthlyFigure(t *testing.T) {
	rows := []EffortRow{
		row("codex", "gpt-5.5", "medium", 40, 70, 5, 4, 0.50),
		row("codex", "gpt-5.5", "high", 70, 70, 5, 4, 1.50),
	}
	got := SuggestEffort(rows, 0)
	if len(got) != 1 || got[0].MonthlySavingUSD != 0 {
		t.Fatalf("got %+v", got)
	}
}

// ByEffort keeps clients apart: the same model at the same level in two
// clients is two rows.
func TestByEffortKeepsClientsApart(t *testing.T) {
	t0 := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	a := synthUnits("a", "m", "high", 3, t0)
	b := synthUnits("b", "m", "high", 2, t0)
	for i := range a {
		a[i].Harness = SourceClaudeCode
	}
	for i := range b {
		b[i].Harness = SourceOpencode
	}
	rows := ByEffort(append(a, b...))
	if len(rows) != 2 || rows[0].Client == rows[1].Client {
		t.Fatalf("rows = %+v", rows)
	}
}
