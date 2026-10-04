package cards

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

func sample() headroom.Glance {
	balance := 12.5
	var g headroom.Glance
	g.Insight.Summary = "Codex is nearest its limit."
	g.Headroom.Reports = []plans.HeadroomReport{
		{Provider: "anthropic", Display: "Claude Max 20x", OverageRisk: plans.RiskLow, Windows: []plans.VendorWindow{
			{Name: "5h", UsedPct: 4, ResetsIn: "4h37m0s"}, {Name: "week (Fable)", UsedPct: 0, ResetsIn: "137h0m0s"}}},
		{Provider: "openai", Display: "ChatGPT Pro Standard ($100) with a long name", OverageRisk: plans.RiskHigh,
			Windows: []plans.VendorWindow{{Name: "week", UsedPct: 91, ResetsIn: "30m0s"}}},
		{Provider: "fireworks", Display: "Pay as you go", SpendUSD: 41.5, SpendLimitUSD: 100, SpendPct: 41.5, BalanceUSD: &balance},
	}
	return g
}

// Every line of every card is exactly as wide, whatever it holds.
func TestCardsKeepTheirWidth(t *testing.T) {
	out := Render(sample(), Options{Width: 40, Now: time.Now()})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")[2:]
	for _, l := range lines {
		if strings.HasPrefix(l, "!") {
			continue
		}
		if n := utf8.RuneCountInString(l); n != cardWidth {
			t.Errorf("line %q is %d wide, want %d", l, n, cardWidth)
		}
	}
	for _, want := range []string{"week (Fable)", "5d 17h"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("plain output carries escapes")
	}
}

// The busiest plan comes first, and the grid fits the terminal.
func TestCardsFillTheWidth(t *testing.T) {
	narrow := Render(sample(), Options{Width: 40})
	if strings.Index(narrow, "ChatGPT") > strings.Index(narrow, "Claude") {
		t.Error("the busiest plan is not first")
	}
	wide := Render(sample(), Options{Width: 120})
	first := strings.Split(wide, "\n")[2]
	if strings.Count(first, "╭") != 3 {
		t.Errorf("120 columns hold three cards per row: %q", first)
	}
	if !strings.Contains(wide, "$41.50/$100") || !strings.Contains(wide, "$12.50 credit left") {
		t.Errorf("spend and balance missing:\n%s", wide)
	}
}

func TestBriefAndUnconfigured(t *testing.T) {
	b := Render(sample(), Options{Brief: true})
	if !strings.HasPrefix(b, "PLAN") || !strings.Contains(b, "week (Fable)") || !strings.Contains(b, " 91%") {
		t.Errorf("brief:\n%s", b)
	}
	var g headroom.Glance
	g.Headroom.Unconfigured = "bind a plan"
	if out := Render(g, Options{}); !strings.Contains(out, "bind a plan") {
		t.Errorf("unconfigured:\n%s", out)
	}
}

func TestColorModes(t *testing.T) {
	if out := Render(sample(), Options{Width: 80, Color: TrueColor}); !strings.Contains(out, "\x1b[38;2;") {
		t.Error("true colour uses no 24-bit escapes")
	}
	basic := Render(sample(), Options{Width: 80, Color: Basic})
	if strings.Contains(basic, "38;2;") || !strings.Contains(basic, "\x1b[91m") {
		t.Error("16-colour output is wrong")
	}
	if c := blend(0); c != okC {
		t.Errorf("blend(0) = %v", c)
	}
	if c := blend(100); c != dangerC {
		t.Errorf("blend(100) = %v", c)
	}
}
