package allocation

import (
	"strings"
	"testing"
)

func TestEvaluateFinalizesEachKeyByTheFirstRuleThatSetsIt(t *testing.T) {
	rules := []Rule{
		{Match: Match{Dir: "/work/clients/acme"}, Tags: Tags{Project: "storefront", Client: "acme"}},
		{Match: Match{Repo: "acme/*"}, Tags: Tags{Project: "acme-other", CostCenter: "cc-acme"}},
		{Match: Match{BranchPrefix: "spike/"}, Tags: Tags{Project: "research"}},
		{Match: Match{Harness: "codex"}, Tags: Tags{CostCenter: "cc-codex"}},
	}
	cases := []struct {
		name string
		s    Subject
		want Tags
	}{
		{
			name: "a directory under the rule's, and a later rule fills only the key left unset",
			s:    Subject{Dir: "/work/clients/acme/web", Repos: []string{"acme/web", "web"}, Harness: "claude-code"},
			want: Tags{Project: "storefront", Client: "acme", CostCenter: "cc-acme"},
		},
		{
			name: "a repository glob on the origin name",
			s:    Subject{Dir: "/elsewhere/api", Repos: []string{"acme/api", "api"}},
			want: Tags{Project: "acme-other", CostCenter: "cc-acme"},
		},
		{
			name: "a branch prefix",
			s:    Subject{Dir: "/elsewhere/x", Branch: "spike/try-it"},
			want: Tags{Project: "research"},
		},
		{
			name: "the harness, case-insensitively, with no directory known",
			s:    Subject{Harness: "Codex"},
			want: Tags{CostCenter: "cc-codex"},
		},
		{
			name: "nothing matches",
			s:    Subject{Dir: "/home/me/notes", Branch: "main", Harness: "claude-code"},
			want: Tags{},
		},
		{
			name: "a sibling directory sharing a prefix is not under the rule's",
			s:    Subject{Dir: "/work/clients/acme-two"},
			want: Tags{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Evaluate(rules, c.s); got != c.want {
				t.Errorf("Evaluate = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestEveryCriterionOfARuleMustHold(t *testing.T) {
	rules := []Rule{{Match: Match{Dir: "/work/*", Harness: "codex"}, Tags: Tags{Client: "globex"}}}
	if got := Evaluate(rules, Subject{Dir: "/work/x", Harness: "claude-code"}); !got.Empty() {
		t.Errorf("a rule matched on its directory alone: %+v", got)
	}
	if got := Evaluate(rules, Subject{Dir: "/work/x/deep", Harness: "codex"}); got.Client != "globex" {
		t.Errorf("a rule did not match with every criterion holding: %+v", got)
	}
}

func TestADirectoryGlobMatchesBelowWhatItMatches(t *testing.T) {
	for _, c := range []struct {
		pattern, dir string
		want         bool
	}{
		{"/src/*/infra", "/src/acme/infra", true},
		{"/src/*/infra", "/src/acme/infra/terraform", true},
		{"/src/*/infra", "/src/acme/app", false},
		{"/src/acme/**", "/src/acme/deep/er", true},
		{"/src/acme/**", "/src/acme", true},
		{"/src/acme", "", false},
	} {
		if got := matchDir(c.pattern, c.dir); got != c.want {
			t.Errorf("matchDir(%q, %q) = %v, want %v", c.pattern, c.dir, got, c.want)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := Rule{Match: Match{Repo: "acme/*"}, Tags: Tags{Client: "Acme Corp"}}
	if err := Validate([]Rule{ok}); err != nil {
		t.Fatalf("valid rule refused: %v", err)
	}
	for _, c := range []struct {
		name string
		r    Rule
		want string
	}{
		{"no criterion", Rule{Tags: Tags{Client: "acme"}}, "match"},
		{"no tag", Rule{Match: Match{Repo: "acme/*"}}, "tag"},
		{"a malformed glob", Rule{Match: Match{Dir: "/src/[acme"}, Tags: Tags{Client: "acme"}}, "dir"},
		{"a relative directory", Rule{Match: Match{Dir: "src/acme"}, Tags: Tags{Client: "acme"}}, "absolute"},
		{"a value too long", Rule{Match: Match{Harness: "codex"}, Tags: Tags{Project: strings.Repeat("x", 65)}}, "64"},
		{"a control character", Rule{Match: Match{Harness: "codex"}, Tags: Tags{Project: "a\nb"}}, "control"},
		{"padding", Rule{Match: Match{Harness: "codex"}, Tags: Tags{Project: " acme"}}, "space"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := Validate([]Rule{ok, c.r})
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "rule 2") {
				t.Errorf("Validate = %v, want an error naming rule 2 and %q", err, c.want)
			}
		})
	}
}

func TestParseKeyAndScope(t *testing.T) {
	for in, want := range map[string]Key{"project": Project, "Client": Client, "cost_center": CostCenter, "cost-center": CostCenter} {
		if got, err := ParseKey(in); err != nil || got != want {
			t.Errorf("ParseKey(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseKey("team"); err == nil {
		t.Error("ParseKey accepted an unknown key")
	}
	s, err := ParseScope("client=Acme Corp")
	if err != nil || s.Key != Client || s.Value != "Acme Corp" {
		t.Errorf("ParseScope = %+v, %v", s, err)
	}
	if s.String() != "client=Acme Corp" {
		t.Errorf("String = %q", s.String())
	}
	if !s.Holds(Tags{Client: "Acme Corp"}) || s.Holds(Tags{Client: "acme"}) {
		t.Error("Holds compares the value exactly")
	}
	for _, bad := range []string{"client", "client=", "team=x", "=x"} {
		if _, err := ParseScope(bad); err == nil {
			t.Errorf("ParseScope(%q) accepted", bad)
		}
	}
}

func TestTagsMapHoldsOnlyTheSetKeys(t *testing.T) {
	got := Tags{Project: "p", CostCenter: "c"}.Map()
	if len(got) != 2 || got["project"] != "p" || got["cost_center"] != "c" {
		t.Errorf("Map = %v", got)
	}
	if (Tags{}).Map() != nil {
		t.Error("an empty set maps to nil")
	}
}
