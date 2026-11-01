package routeguard

import (
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

var tipCandidates = []string{"claude-haiku-4-5", "claude-sonnet-5", "claude-opus-5"}

func rank(model string) int {
	return ModelRank(model, eventschema.ProviderAnthropic, catalog(), tipCandidates)
}

func TestSubagentType(t *testing.T) {
	for _, tc := range []struct {
		in   map[string]any
		want string
	}{
		{map[string]any{"subagent_type": "Explore"}, "Explore"},
		{map[string]any{"subagent_type": "  "}, DefaultSubagentType},
		{map[string]any{}, DefaultSubagentType},
	} {
		if got := SubagentType(tc.in); got != tc.want {
			t.Errorf("SubagentType(%v) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// The next launch of the tip's type settles it: below the tier it named
// is following, the same tier or above is ignoring, with what was
// launched instead. Another type, or a model that cannot be placed,
// settles nothing.
func TestSettleSubagentTips(t *testing.T) {
	cases := []struct {
		name     string
		typ      string
		model    string
		settled  bool
		followed bool
		instead  string
	}{
		{"a cheaper launch follows it", "Explore", "haiku", true, true, ""},
		{"the same model again ignores it", "Explore", "opus", true, false, "launched Explore on opus again"},
		{"a model id is named by its alias", "Explore", "claude-opus-5", true, false, "launched Explore on opus again"},
		{"another type settles nothing", "Plan", "opus", false, false, ""},
		{"an unplaceable model settles nothing", "Explore", "mystery-model", false, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			OpenSubagentTip(dir, "s1", SubagentTip{ID: "t1", Type: "Explore", FromRank: rank("opus"), To: "claude-haiku-4-5"})
			got := SettleSubagentTips(dir, "s1", c.typ, c.model, rank(c.model))
			if !c.settled {
				if len(got) != 0 {
					t.Fatalf("settled %+v; want none", got)
				}
				return
			}
			if len(got) != 1 || got[0].ID != "t1" || got[0].Followed != c.followed || got[0].Instead != c.instead {
				t.Fatalf("settled %+v; want followed=%v instead %q", got, c.followed, c.instead)
			}
			if again := SettleSubagentTips(dir, "s1", c.typ, c.model, rank(c.model)); len(again) != 0 {
				t.Fatalf("a settled tip settled again: %+v", again)
			}
		})
	}
}

// A newer tip for a type replaces the older one.
func TestOpenSubagentTipReplacesTheSameType(t *testing.T) {
	dir := t.TempDir()
	OpenSubagentTip(dir, "s1", SubagentTip{ID: "old", Type: "Explore", FromRank: rank("opus")})
	OpenSubagentTip(dir, "s1", SubagentTip{ID: "new", Type: "Explore", FromRank: rank("opus")})
	got := SettleSubagentTips(dir, "s1", "Explore", "opus", rank("opus"))
	if len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("settled %+v; want only the newer tip", got)
	}
}

// Advice the throttle holds is not given and not argued, so it can be
// given later in the session.
func TestEvaluateHoldIsNotArgued(t *testing.T) {
	dir := t.TempDir()
	in := Input{
		Dir: dir, SessionID: "s1", Prompt: "show me the retention config", CurrentModel: "claude-opus-5",
		Provider: eventschema.ProviderAnthropic, Mode: ModeAdvise, Catalog: catalog(), Candidates: tipCandidates,
		NewID: func() string { return "a1" },
	}
	held := in
	held.Hold = func(Kind, string) bool { return true }
	if d := Evaluate(held); d.Advise {
		t.Fatalf("held advice was given: %+v", d)
	}
	if d := Evaluate(in); !d.Advise || d.OfferID != "a1" {
		t.Fatalf("advice after the hold = %+v; want it given", d)
	}
}
