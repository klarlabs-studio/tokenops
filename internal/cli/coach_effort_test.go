package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/effortweeks"
)

func TestRenderEffortWatch(t *testing.T) {
	at := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		w    effortweeks.Watch
		want []string
		not  []string
	}{
		{name: "waiting says how many weeks",
			w:    effortweeks.Watch{Client: "codex", Model: "gpt-5.5", Verdict: effortweeks.Verdict{Change: effortweeks.Change{At: at, From: "high", To: "medium"}, Reason: "waiting for 2 more weeks since the change; have 2"}},
			want: []string{"gpt-5.5 (codex) high → medium from the week of 2026-10-05", "waiting for 2 more weeks"},
			not:  []string{"cost per instruction", "prevented"}},
		{name: "a realized saving shows intervals and an estimate",
			w: effortweeks.Watch{Model: "m", PreventedUSDPerWeek: 35, Verdict: effortweeks.Verdict{
				Change: effortweeks.Change{At: at, From: "xhigh", To: "medium"}, Judged: true, Realized: true, Reason: "the cost per instruction fell",
				Cost:     effortweeks.Measure{Before: effortweeks.Interval{Mean: 1.2, Low: 1.1, High: 1.3}, After: effortweeks.Interval{Mean: 0.5, Low: 0.45, High: 0.55}},
				FirstTry: effortweeks.Measure{Before: effortweeks.Interval{Mean: 70, Low: 65, High: 75}, After: effortweeks.Interval{Mean: 71, Low: 66, High: 76}},
			}},
			want: []string{"cost per instruction $1.20 ($1.10–$1.30) → $0.50 ($0.45–$0.55)", "first try 70% (65–75) → 71% (66–76)", "$35.00 a week prevented (an estimate", "not an experiment"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			renderEffortWatch(&b, []effortweeks.Watch{tt.w})
			out := b.String()
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in:\n%s", w, out)
				}
			}
			for _, w := range tt.not {
				if strings.Contains(out, w) {
					t.Errorf("unexpected %q in:\n%s", w, out)
				}
			}
		})
	}
}
