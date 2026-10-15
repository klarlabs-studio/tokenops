package allocation

import (
	"context"
	"math"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	dom "go.klarlabs.de/tokenops/internal/contexts/spend/allocation"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
)

var t0 = time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

func turn(session, source string, at time.Duration, usd float64) analytics.SessionTurn {
	return analytics.SessionTurn{SessionID: session, Source: source, At: t0.Add(at), Tokens: 1000,
		APIEquivalentUSD: usd, Priced: true}
}

func matchDir(d string) dom.Match     { return dom.Match{Dir: d} }
func matchRepo(r string) dom.Match    { return dom.Match{Repo: r} }
func matchHarness(h string) dom.Match { return dom.Match{Harness: h} }

var rules = []Rule{
	{Match: matchDir("/w/clients/acme"), Tags: Tags{Project: "storefront", Client: "acme"}},
	{Match: matchRepo("globex/*"), Tags: Tags{Client: "globex"}},
	{Match: matchHarness("codex"), Tags: Tags{CostCenter: "cc-codex"}},
}

func TestTagMatchesEachSessionByWhereItRan(t *testing.T) {
	place := Place{
		Dirs: map[string]sessiondirs.Dir{
			"a": {CWD: "/w/clients/acme/web"}, "b": {CWD: "/w/gx/api"}, "c": {CWD: "/tmp"},
		},
		RootOf: map[string]string{"/w/clients/acme/web": "/w/clients/acme/web", "/w/gx/api": "/w/gx/api", "/tmp": ""},
		Names:  map[string][]string{"/w/gx/api": {"globex/api", "api"}},
	}
	turns := []analytics.SessionTurn{
		turn("a", "claude-code-jsonl", 0, 2), turn("a", "claude-code-jsonl", time.Hour, 3),
		turn("b", "codex-jsonl", 0, 5),
		turn("c", "claude-code-jsonl", 0, 7),
		turn("z", "codex-jsonl", 0, 11), // no directory known: only the harness rule can match
	}
	got := Tag(rules, turns, place)
	want := []Tags{
		{Project: "storefront", Client: "acme"}, {Project: "storefront", Client: "acme"},
		{Client: "globex", CostCenter: "cc-codex"},
		{},
		{CostCenter: "cc-codex"},
	}
	for i, tt := range got {
		if tt.Tags != want[i] {
			t.Errorf("turn %d (%s) tags = %+v, want %+v", i, tt.SessionID, tt.Tags, want[i])
		}
	}
	if got[4].DirKnown || !got[0].DirKnown {
		t.Error("DirKnown is wrong")
	}

	r := ByKey(Client, got)
	// Equal value: by name.
	if len(r.Groups) != 2 || r.Groups[1].Value != "globex" || r.Groups[1].APIEquivalentUSD != 5 ||
		r.Groups[0].Value != "acme" || r.Groups[0].APIEquivalentUSD != 5 || r.Groups[0].Turns != 2 || r.Groups[0].Sessions != 1 {
		t.Errorf("groups = %+v", r.Groups)
	}
	if r.Untagged.APIEquivalentUSD != 18 || r.UnknownDirectory.APIEquivalentUSD != 11 {
		t.Errorf("untagged = %+v, unknown = %+v", r.Untagged, r.UnknownDirectory)
	}
	if want := 10.0 / 28; math.Abs(r.TaggedShare-want) > 1e-9 {
		t.Errorf("tagged share = %v, want %v", r.TaggedShare, want)
	}

	c := CoverageOf(len(rules), got)
	if math.Abs(c.TaggedPct-21.0/28*100) > 1e-9 || math.Abs(c.ByKeyPct["cost_center"]-16.0/28*100) > 1e-9 {
		t.Errorf("coverage = %+v", c)
	}
}

func TestComputeKeepsTheWindowAndCountsTheRules(t *testing.T) {
	d := Deps{
		Turns: func(context.Context, time.Time) ([]analytics.SessionTurn, error) {
			return []analytics.SessionTurn{turn("z", "codex-jsonl", 0, 1), turn("z", "codex-jsonl", 48*time.Hour, 2)}, nil
		},
		Dirs:  func(time.Time) map[string]sessiondirs.Dir { return nil },
		Rules: rules,
	}
	r, err := Compute(context.Background(), d, CostCenter, t0, t0.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if r.Rules != 3 || len(r.Groups) != 1 || r.Groups[0].APIEquivalentUSD != 1 || r.TaggedShare != 1 {
		t.Errorf("report = %+v", r)
	}
	none, err := Compute(context.Background(), Deps{Turns: d.Turns}, Project, t0, t0.Add(72*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if none.Rules != 0 || len(none.Groups) != 0 || none.Untagged.Turns != 2 {
		t.Errorf("without rules = %+v", none)
	}
}

func TestHarnessOf(t *testing.T) {
	for src, want := range map[string]string{
		"claude-code-jsonl": "claude-code", "claude-code-statusline": "claude-code", "codex-jsonl": "codex",
		"codex-app-server": "codex", "opencode": "opencode", "cursor-hook": "cursor", "pi-sessions": "pi",
		"something-else": "something-else",
	} {
		if got := HarnessOf(src); got != want {
			t.Errorf("HarnessOf(%q) = %q, want %q", src, got, want)
		}
	}
}
