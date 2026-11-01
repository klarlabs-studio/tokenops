package routeguard

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/modeltier"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func priceAt(lambda float64) *QuotaPrice {
	return &QuotaPrice{Vendor: "Claude", Window: "weekly", Lambda: lambda, LeftPct: 30, Basis: "pace"}
}

// A tier up is taken only while its quality gain exceeds its λ-weighted
// extra plan use. In the test catalog Sonnet costs 40% of Opus (60% less
// plan use) and Opus half of Fable, so an edit (gain 0.12) steps down from
// Opus once λ passes 0.2, and deep work (gain 0.20) from Fable once λ
// passes 0.4.
func TestEscalationFollowsThePrice(t *testing.T) {
	const (
		edit = "add a test for the parser"
		deep = "debug why the router drops requests"
	)
	for _, tc := range []struct {
		name, prompt, requested, session string
		lambda                           float64
		want                             string // alias; "" is no move
		escalated                        bool
	}{
		{"edit, slack", edit, "opus", "", 0, "", true},
		{"edit, low price", edit, "opus", "", 0.1, "", true},
		{"edit, just under", edit, "opus", "", 0.19, "", true},
		{"edit, just over", edit, "opus", "", 0.21, "sonnet", false},
		{"edit, high price", edit, "opus", "", 0.8, "sonnet", false},
		{"edit, used up", edit, "opus", "", 1, "sonnet", false},
		{"deep, low price", deep, "", "claude-fable-5-1", 0.3, "", true},
		{"deep, high price", deep, "", "claude-fable-5-1", 0.5, "opus", false},
		// From Fable an edit's calm move is to Opus; the price decides
		// whether it goes on to Sonnet.
		{"edit on fable, low price", edit, "", "claude-fable-5-1", 0.1, "opus", true},
		{"edit on fable, high price", edit, "", "claude-fable-5-1", 0.5, "sonnet", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := subagent(tc.prompt, tc.requested, tc.session)
			in.Price = priceAt(tc.lambda)
			d := EvaluateSubagent(in)
			if d.ToAlias != tc.want {
				t.Fatalf("moved to %q, want %q (%+v)", d.ToAlias, tc.want, d)
			}
			if d.Escalation == nil || d.Escalation.Escalated != tc.escalated {
				t.Fatalf("escalation = %+v, want escalated %v", d.Escalation, tc.escalated)
			}
			if d.Escalation.Lambda != tc.lambda || d.Escalation.Window != "Claude weekly" {
				t.Errorf("escalation does not carry its price: %+v", d.Escalation)
			}
			// The price is credited only where it changed the move.
			if (d.Price != nil) != !tc.escalated {
				t.Errorf("price credited = %v", d.Price != nil)
			}
			if ev := d.Evidence(); !strings.Contains(ev, "quota_price: λ") || !strings.Contains(ev, "escalation: ") {
				t.Errorf("evidence = %q", ev)
			}
		})
	}
}

// The per-turn advice weighs the same choice the same way.
func TestTurnEscalationFollowsThePrice(t *testing.T) {
	for _, tc := range []struct {
		lambda float64
		advise bool
	}{{0, false}, {0.1, false}, {0.19, false}, {0.21, true}, {0.9, true}} {
		in := input(t, "fix the typo in the README", "claude-opus-5", ModeAdvise)
		in.Price = priceAt(tc.lambda)
		got := Evaluate(in)
		if got.Advise != tc.advise || (tc.advise && got.To != "claude-sonnet-5") {
			t.Errorf("λ %v: %+v", tc.lambda, got)
		}
		if got.Escalation == nil || got.Escalation.Escalated == tc.advise {
			t.Errorf("λ %v: escalation %+v", tc.lambda, got.Escalation)
		}
	}
}

// A price of 0 routes exactly as no price does: the plan has room, so the
// work alone places every subagent.
func TestSlackRoutesAsNoPrice(t *testing.T) {
	prompts := []string{
		"find where the retention config is defined", "research how prefix keys work and summarise",
		"add a test for the parser", "debug why the router drops requests",
	}
	for _, p := range prompts {
		for _, req := range []string{"haiku", "sonnet", "opus", ""} {
			in := subagent(p, req, "claude-fable-5-1")
			calm := EvaluateSubagent(in)
			in.Price = priceAt(0)
			slack := EvaluateSubagent(in)
			if calm.To != slack.To || calm.Reason != slack.Reason || slack.Price != nil {
				t.Errorf("%q from %q: calm %q, slack %q", p, req, calm.To, slack.To)
			}
		}
	}
}

// The quota cost of the tier up is λ times the share of its plan use the
// tier below saves, read from the rate card.
func TestExtraUse(t *testing.T) {
	for name, tc := range map[string]struct {
		upper, lower modeltier.Resolution
		want         float64
	}{
		"priced":          {modeltier.Resolution{CostPerMillion: 30}, modeltier.Resolution{CostPerMillion: 12}, 0.6},
		"upper free":      {modeltier.Resolution{Free: true}, modeltier.Resolution{CostPerMillion: 12}, 0},
		"lower free":      {modeltier.Resolution{CostPerMillion: 30}, modeltier.Resolution{Free: true}, 1},
		"unpriced":        {modeltier.Resolution{}, modeltier.Resolution{}, defaultExtraUse},
		"lower is dearer": {modeltier.Resolution{CostPerMillion: 10}, modeltier.Resolution{CostPerMillion: 12}, 0},
	} {
		if got := extraUse(tc.upper, tc.lower); got < tc.want-1e-9 || got > tc.want+1e-9 {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

// ReadQuotaPrice prefers the status line's reading, falls back to the
// daemon's figures, and reads nothing as no price.
func TestReadQuotaPrice(t *testing.T) {
	dir := t.TempDir()
	limits := dir + "/claude-limits.json"
	figs := boundaryledger.Dir(dir + "/boundaries")
	if p := ReadQuotaPrice(limits, figs, pressureNow); p != nil {
		t.Fatalf("nothing read: %+v", p)
	}
	if err := figs.PutFigures(boundaryledger.Figures{ObservedAt: pressureNow.Add(-time.Minute),
		PlanLeftPct: map[string]map[string]float64{"anthropic": {"week": 10}}}); err != nil {
		t.Fatal(err)
	}
	if p := ReadQuotaPrice(limits, figs, pressureNow); p == nil || p.Source != "daemon_glance" || p.Basis != "share_left" || p.Lambda < 0.69 {
		t.Fatalf("figures fallback: %+v", p)
	}
	if err := claudelimits.Write(limits, reading(window(10, 4*time.Hour), window(46, 3*24*time.Hour+18*time.Hour))); err != nil {
		t.Fatal(err)
	}
	p := ReadQuotaPrice(limits, figs, pressureNow)
	if p == nil || p.Source != "claude_code_statusline" || p.Lambda != 0 {
		t.Fatalf("a fresh status line reading on pace answers with no price: %+v", p)
	}
}

// Escalations say, per kind of work the price can move, which way the
// choice goes now, for `tokenops explain price`.
func TestEscalations(t *testing.T) {
	cands := []string{"claude-haiku-4-5", "claude-sonnet-5", "claude-opus-5", "claude-fable-5-1"}
	got := Escalations(maintainerPrice(), catalog(), eventschema.ProviderAnthropic, cands)
	if len(got) != 2 {
		t.Fatalf("escalations = %+v", got)
	}
	want := map[taskclass.Kind][2]string{
		taskclass.KindEdit: {"claude-opus-5", "claude-sonnet-5"},
		taskclass.KindDeep: {"claude-fable-5-1", "claude-opus-5"},
	}
	for _, e := range got {
		if w := want[e.Kind]; e.Upper != w[0] || e.Lower != w[1] || e.Escalated {
			t.Errorf("%s: %+v", e.Kind, e)
		}
		if !strings.Contains(e.Why(), "not escalated") {
			t.Errorf("why = %q", e.Why())
		}
	}
	if got := Escalations(priceAt(0), catalog(), eventschema.ProviderAnthropic, cands); len(got) != 2 || !got[0].Escalated || !got[1].Escalated {
		t.Errorf("slack escalations = %+v", got)
	}
	if got := Escalations(priceAt(1), nil, eventschema.ProviderAnthropic, cands); got != nil {
		t.Errorf("no catalog: %+v", got)
	}
}
