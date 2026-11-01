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
	if len(got) != 1 || !strings.Contains(got[0], lime+"●") {
		t.Fatalf("plain tip %q", got)
	}
	in.Tip, in.TipUrgent = "Claude weekly 10% left → /compact, smaller model", true
	got = Render(in)
	if !strings.Contains(got[0], warnFg+"● Claude weekly 10% left") {
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
	if !strings.Contains(line, lime+"Opus 5.5") {
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

// The line warns only when the cache is about to go cold, and says so
// once it has; the rest of the time it shows the hit ratio.
func TestRenderCacheCountdown(t *testing.T) {
	cases := []struct {
		name    string
		left    time.Duration
		unknown bool
		tokens  int64
		want    string
		absent  []string
	}{
		{name: "with what it holds", left: 70 * time.Second, tokens: 182_400, want: "cache cold in 2m (182k)"},
		{name: "cold, with what the next prompt rewrites", left: -time.Minute, tokens: 1_240_000, want: "cache cold (1.2M)"},
		{name: "a small cache", left: -time.Minute, tokens: 950, want: "cache cold (950)"},
		{name: "what it holds is not shown while warm", left: 4 * time.Minute, tokens: 182_400, want: "cache 95%", absent: []string{"182k"}},
		{name: "unknown expiry", unknown: true, want: "cache 95%", absent: []string{"cold"}},
		{name: "plenty left", left: 4 * time.Minute, want: "cache 95%", absent: []string{"cold"}},
		{name: "exactly two minutes", left: 2 * time.Minute, want: "cache 95%", absent: []string{"cold"}},
		{name: "under two minutes", left: 119 * time.Second, want: "cache cold in 2m", absent: []string{"cache 95%"}},
		{name: "under a minute", left: 20 * time.Second, want: "cache cold in 1m", absent: []string{"cache 95%"}},
		{name: "at expiry", left: 0, want: "cache cold", absent: []string{"cold in", "cache 95%"}},
		{name: "long gone", left: -3 * time.Hour, want: "cache cold", absent: []string{"cold in"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := full()
			in.CacheTokens = c.tokens
			if !c.unknown {
				in.CacheExpiresAt = now.Add(c.left)
			}
			line := Render(in)[0]
			if !strings.Contains(line, c.want) {
				t.Errorf("line %q lacks %q", line, c.want)
			}
			for _, a := range c.absent {
				if strings.Contains(line, a) {
					t.Errorf("line %q has %q", line, a)
				}
			}
		})
	}
	// Coloured as a warning.
	in := full()
	in.Color, in.CacheExpiresAt = true, now.Add(time.Minute)
	if line := Render(in)[0]; !strings.Contains(line, warnFg+"cache cold in 1m") {
		t.Errorf("the warning is not coloured: %q", line)
	}
}
