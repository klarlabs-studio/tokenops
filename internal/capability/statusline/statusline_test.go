package statusline

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/fx"
)

var now = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

func full() Input {
	return Input{
		Model: "Opus 5.5", Effort: "high",
		ContextPct: 71, CompactPct: 80, CacheHit: 0.95,
		CostUSD: 3.53, Covered: true,
		Windows: []Window{
			{Label: "5h", UsedPct: 62, ResetsAt: now.Add(5*time.Hour + 10*time.Minute)},
			{Label: "wk", UsedPct: 41, ResetsAt: now.Add(72 * time.Hour)},
		},
		Rate: fx.Rate{Currency: "EUR", PerUSD: 0.885, Source: fx.SourceECB},
		Tip:  "compact now (/compact)",
		Now:  now,
	}
}

func TestRenderPlain(t *testing.T) {
	got := Render(full())
	if len(got) != 1 {
		t.Fatalf("lines %q", got)
	}
	for _, part := range []string{"Opus 5.5 high", "▰▰▱▱▱▱ 5h 38% left ↻14:10", "wk 59% left ↻Mon", "ctx 71% → 80%", "cache 95%", "€3.12 value"} {
		if !strings.Contains(got[0], part) {
			t.Errorf("line %q lacks %q", got[0], part)
		}
	}
	if strings.Contains(got[0], "\x1b[") {
		t.Error("colour codes without Color")
	}
	if !strings.HasSuffix(got[0], " · ● compact now (/compact)") {
		t.Errorf("the open tip does not end the line: %q", got[0])
	}
}

// The tip shares the one line the operator sees every turn, and an
// urgent one (work about to stop) is coloured as a warning.
func TestRenderTipOnTheLine(t *testing.T) {
	in := full()
	in.Color = true
	got := Render(in)
	if len(got) != 1 || !strings.Contains(got[0], cobalt+"●") {
		t.Fatalf("plain tip %q", got)
	}
	in.Tip, in.TipUrgent = "Claude weekly 90% used → /compact, smaller model", true
	got = Render(in)
	if !strings.Contains(got[0], warnFg+"● Claude weekly 90% used") {
		t.Errorf("urgent tip is not a warning: %q", got[0])
	}
	in.Tip = ""
	if got := Render(in); strings.Contains(got[0], "●") {
		t.Errorf("no tip, yet a marker: %q", got[0])
	}
}

func TestRenderColoursFollowTheLimit(t *testing.T) {
	in := full()
	in.Color = true
	in.Windows = []Window{{Label: "5h", UsedPct: 85}}
	line := Render(in)[0]
	if !strings.Contains(line, dangerFg+"15% left") {
		t.Errorf("85%% used is not danger-coloured: %q", line)
	}
	if !strings.Contains(line, cobaltFg+"Opus 5.5") {
		t.Error("the model is not in the Klarlabs accent")
	}
	// 71% of the window is 89% of the way to compacting at 80%: warn red.
	if !strings.Contains(line, dangerFg+"71%") {
		t.Errorf("context is not measured against the compaction point: %q", line)
	}
}

func TestRenderOmitsWhatIsUnknown(t *testing.T) {
	got := Render(Input{CacheHit: -1, Now: now})
	if len(got) != 1 || got[0] != "" {
		t.Errorf("an empty input rendered %q", got)
	}
	in := Input{CostUSD: 1, CacheHit: -1, Now: now}
	if got := Render(in)[0]; got != "$1.00" {
		t.Errorf("billed USD cost %q", got)
	}
}

func TestResetIn(t *testing.T) {
	if got := resetIn(now.Add(20*time.Minute), now); got != "21m" {
		t.Errorf("soon: %q", got)
	}
	if got := resetIn(now.Add(3*time.Hour), now); got != "12:00" {
		t.Errorf("today: %q", got)
	}
}

func TestSubagentRow(t *testing.T) {
	got := SubagentRow(Subagent{Name: "Explore", Model: "claude-haiku-4-5-20251001", Effort: "low", ContextPct: 30})
	if got != "Explore · haiku-4-5 low · ▰▱▱▱ 30%" {
		t.Errorf("row %q", got)
	}
	if got := SubagentRow(Subagent{Name: "plan", ContextPct: -1}); got != "plan" {
		t.Errorf("unresolved model row %q", got)
	}
}
