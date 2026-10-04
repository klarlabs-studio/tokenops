package cards

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

func sample() headroom.Glance {
	balance := 12.5
	var g headroom.Glance
	g.Insight.Summary = "Claude Max 20x session budget currently recommends continuing."
	g.Headroom.Reports = []plans.HeadroomReport{
		{Provider: "anthropic", Display: "Claude Max 20x", OverageRisk: plans.RiskLow, Windows: []plans.VendorWindow{
			{Name: "5h", UsedPct: 4, ResetsIn: "4h37m0s", Pace: &plans.WindowPace{Status: plans.PaceBehind, DeltaPct: -3, LastsToReset: true}},
			{Name: "week (Fable)", UsedPct: 0, ResetsIn: "137h0m0s"}}},
		{Provider: "openai", Display: "ChatGPT Pro Standard ($100)", OverageRisk: plans.RiskHigh,
			SignalQuality: plans.SignalQuality{Source: "codex_jsonl"},
			Windows: []plans.VendorWindow{{Name: "week", UsedPct: 91, ResetsIn: "30m0s",
				Pace: &plans.WindowPace{Status: plans.PaceAhead, DeltaPct: 37, RunsOutIn: 23*time.Hour + 47*time.Minute}}}},
		{Provider: "fireworks", Display: "Pay as you go", SpendUSD: 41.5, SpendLimitUSD: 100, SpendPct: 41.5, BalanceUSD: &balance},
	}
	return g
}

var costs = map[string]spending.ProviderCost{
	"openai": {
		Today:  spending.Usage{Tokens: 198e6, APIEquivalentUSD: 28, Requests: 100, UnpricedRequests: 15},
		Last30: spending.Usage{Tokens: 2_240_000_000, APIEquivalentUSD: 745, Requests: 1000, UnpricedRequests: 150},
	},
	"anthropic": {
		Today:  spending.Usage{Tokens: 1e9, APIEquivalentUSD: 400, Requests: 10, UnpricedRequests: 9},
		Last30: spending.Usage{Tokens: 2e10, APIEquivalentUSD: 10948, CostUSD: 0, Requests: 100},
	},
}

func lines(out string) []string { return strings.Split(strings.TrimRight(out, "\n"), "\n") }

// Every line of a row of cards is as wide as the row, whatever it holds.
func TestCardsKeepTheirWidth(t *testing.T) {
	for _, width := range []int{40, 80, 86, 130} {
		out := Render(sample(), Options{Width: width, Costs: costs})
		perRow, cw := layout(width)
		for _, l := range lines(out)[2:] {
			if l == "" || strings.HasPrefix(l, "!") {
				continue
			}
			n := utf8.RuneCountInString(l)
			if n%(cw+cardGap) != cw || n > perRow*(cw+cardGap) {
				t.Errorf("width %d: line %q is %d wide; cards are %d", width, l, n, cw)
			}
		}
		if strings.Contains(out, "\x1b[") {
			t.Error("plain output carries escapes")
		}
	}
}

// A card reads as CodexBar's: source and plan, then each window's share,
// bar, reset and pace, then the money.
func TestCardContents(t *testing.T) {
	out := Render(sample(), Options{Width: 130, Costs: costs, Now: time.Date(2026, 10, 4, 9, 30, 0, 0, time.UTC)})
	for _, want := range []string{
		"TokenOps • AI Usage & Limits", "Oct",
		"Codex [local]", "PLAN Pro Standard ($100)", "Claude", "PLAN Max 20x",
		"Weekly", "91% used", "Resets in 30m", "Pace: ahead (+37%) · out in 23h 47m",
		"Session", "Pace: behind (-3%) · lasts to reset", "Weekly · Fable", "0% used",
		"Extra usage", "$41.50 / $100", "Credit left:", "$12.50", "Overage risk:", "HIGH",
		"$745+ · 2.24B tok", "15% of requests have no price yet.", "At API prices; the plan covers it.",
		"$10,948 · 20.00B tok",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("cards lack %q:\n%s", want, out)
		}
	}
	// Mostly unpriced shows tokens only: a near-complete-looking figure
	// would mislead.
	if strings.Contains(out, "$400") || !strings.Contains(out, "1.00B tok") {
		t.Errorf("a mostly unpriced day shows money:\n%s", out)
	}
	// The glance-wide insight names one plan; it is not every card's header.
	if strings.Contains(out, "recommends continuing") {
		t.Error("the insight is printed over every plan")
	}
}

// The busiest plan comes first, in the cards and in the table alike, and
// the grid fits the terminal.
func TestOrderAndLayout(t *testing.T) {
	for _, brief := range []bool{false, true} {
		out := Render(sample(), Options{Width: 80, Brief: brief})
		codex, claude := strings.Index(out, "Codex"), strings.Index(out, "Claude")
		if codex < 0 || claude < 0 || codex > claude {
			t.Errorf("brief=%v: the busiest plan is not first:\n%s", brief, out)
		}
	}
	for width, want := range map[int]int{40: 1, 80: 2, 86: 2, 130: 3} {
		if perRow, cw := layout(width); perRow != want || cw < minCardWidth || cw > maxCardWidth {
			t.Errorf("layout(%d) = %d × %d, want %d per row", width, perRow, cw, want)
		}
	}
}

func TestBriefAndUnconfigured(t *testing.T) {
	b := Render(sample(), Options{Brief: true, Width: 100})
	for _, want := range []string{"PLAN", "PACE", "Codex Pro Standard ($100)", "Weekly · Fable", " 91%", "+37% · out in 23h 47m", "-3% · lasts"} {
		if !strings.Contains(b, want) {
			t.Errorf("brief lacks %q:\n%s", want, b)
		}
	}
	var g headroom.Glance
	g.Headroom.Unconfigured = "bind a plan"
	if out := Render(g, Options{}); !strings.Contains(out, "bind a plan") {
		t.Errorf("unconfigured:\n%s", out)
	}
}

func TestColorModes(t *testing.T) {
	if out := Render(sample(), Options{Width: 80, Color: TrueColor}); !strings.Contains(out, "\x1b[48;2;") {
		t.Error("true colour draws no gradient cells")
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
	if money(10948.4) != "$10,948" || money(745) != "$745" || money(4.2) != "$4.20" {
		t.Errorf("money: %s %s %s", money(10948.4), money(745), money(4.2))
	}
}

func TestCoachSection(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	read := now.Add(-3 * time.Hour)
	r := &findings.Report{SessionsReadAt: &read}
	for i := range 8 {
		r.Findings = append(r.Findings, findings.Finding{Level: findings.LevelNotice, Title: fmt.Sprintf("finding %d", i),
			Evidence: "the figures behind it", Action: "Do the thing."})
	}
	out := Render(sample(), Options{Width: 86, Now: now, Findings: r})
	for _, want := range []string{"Coach · 8 findings · sessions read 3h ago", "● finding 0", "the figures behind it", "→ Do the thing.", "+ 2 more: tokenops glance --findings"} {
		if !strings.Contains(out, want) {
			t.Errorf("coach section lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "finding 7") {
		t.Error("more findings than fit are listed")
	}
	all := Render(sample(), Options{Width: 86, Now: now, Findings: r, OnlyFindings: true})
	if !strings.Contains(all, "finding 7") || strings.Contains(all, "╭") {
		t.Errorf("--findings:\n%s", all)
	}
	brief := Render(sample(), Options{Width: 86, Now: now, Findings: r, Brief: true})
	if strings.Contains(brief, "the figures behind it") || !strings.Contains(brief, "finding 0") {
		t.Errorf("brief findings:\n%s", brief)
	}
	if out := Render(sample(), Options{Findings: &findings.Report{}}); !strings.Contains(out, "nothing stands out") {
		t.Errorf("no findings:\n%s", out)
	}
}

func TestWrap(t *testing.T) {
	if got := wrap("one two three four", 9); strings.Join(got, "|") != "one two|three|four" {
		t.Errorf("wrap = %q", got)
	}
	if wrap("", 10) != nil {
		t.Error("empty text wraps to lines")
	}
}

// A cost cut off by the time limit says so rather than vanishing.
func TestLateCostSaysSo(t *testing.T) {
	out := Render(sample(), Options{Width: 130, Costs: map[string]spending.ProviderCost{"openai": costs["openai"]}, CostsLate: true})
	if !strings.Contains(out, "Cost:") || !strings.Contains(out, "not read in time") {
		t.Errorf("late cost silent:\n%s", out)
	}
	if strings.Contains(Render(sample(), Options{Width: 130}), "not read in time") {
		t.Error("a cost never asked for is reported late")
	}
}
