package rules

import (
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func buildCorpus() []*RuleDocument {
	docs := []*RuleDocument{
		{
			SourceID: "repo:CLAUDE.md", Source: eventschema.RuleSourceClaudeMD,
			Scope: eventschema.RuleScopeRepo, Path: "CLAUDE.md", RepoID: "repo",
			Body: "# Testing\nWrite Go tests using table-driven style.\n## Bash\nNever pipe curl to bash.\n",
		},
		{
			SourceID: "repo:.cursor/rules/go.mdc", Source: eventschema.RuleSourceCursorRules,
			Scope: eventschema.RuleScopeFileGlob, Path: ".cursor/rules/go.mdc", RepoID: "repo",
			Body: "# Go style\nPrefer composition; keep functions short.\n",
		},
		{
			SourceID: "global:HOUSE.md", Source: eventschema.RuleSourceCustom,
			Scope: eventschema.RuleScopeGlobal, Path: "HOUSE.md", RepoID: "",
			Body: "# House Rules\nNever leak secrets.\n",
		},
	}
	for _, d := range docs {
		d.Blocks = ParseMarkdown(d.Body)
	}
	return docs
}

func TestRouterKeywordSelection(t *testing.T) {
	docs := buildCorpus()
	r := NewRouter(RouterConfig{MinScore: 0.5})
	res := r.Select(docs, SelectionSignals{
		RepoID:   "repo",
		Keywords: []string{"testing"},
	})
	if len(res.Selections) == 0 {
		t.Fatal("expected at least one selection")
	}
	hit := false
	for _, s := range res.Selections {
		if strings.Contains(s.SectionID, "Testing") {
			hit = true
		}
	}
	if !hit {
		t.Errorf("expected Testing section in selection: %+v", res.Selections)
	}
}

func TestRouterFileGlobScores(t *testing.T) {
	docs := buildCorpus()
	r := NewRouter(RouterConfig{MinScore: 0.5})
	res := r.Select(docs, SelectionSignals{
		RepoID:    "repo",
		FilePaths: []string{"internal/foo/bar.go"},
	})
	if len(res.Selections) == 0 {
		t.Fatal("expected at least one selection from go file path")
	}
	hit := false
	for _, s := range res.Selections {
		if strings.Contains(s.SectionID, "go.mdc") {
			hit = true
		}
	}
	if !hit {
		t.Errorf("expected cursor go rule selection: %+v", res.Selections)
	}
}

func TestRouterRespectsTokenBudget(t *testing.T) {
	docs := buildCorpus()
	r := NewRouter(RouterConfig{MinScore: 0.0, TokenBudget: 3, IncludeGlobalScope: true})
	res := r.Select(docs, SelectionSignals{Keywords: []string{"rules"}})
	if !res.BudgetHit {
		t.Errorf("expected BudgetHit=true with tiny budget; selections=%+v", res.Selections)
	}
	if res.TotalTokens > 3 {
		t.Errorf("TotalTokens = %d exceeds budget 3", res.TotalTokens)
	}
}

func TestRouterIncludeGlobalScope(t *testing.T) {
	docs := buildCorpus()
	r := NewRouter(RouterConfig{MinScore: 1.0, IncludeGlobalScope: true})
	res := r.Select(docs, SelectionSignals{})
	hit := false
	for _, s := range res.Selections {
		if strings.Contains(s.SourceID, "HOUSE.md") {
			hit = true
		}
	}
	if !hit {
		t.Errorf("expected global house rule even without signals: %+v", res.Selections)
	}
}

func TestRouterSkipsRepoMismatch(t *testing.T) {
	docs := buildCorpus()
	r := NewRouter(RouterConfig{MinScore: 0.0, IncludeGlobalScope: true})
	res := r.Select(docs, SelectionSignals{RepoID: "other-repo"})
	for _, s := range res.Selections {
		if strings.HasPrefix(s.SourceID, "repo:") {
			t.Errorf("repo-scoped section leaked to mismatched repo: %s", s.SectionID)
		}
	}
	// Global scope still selected.
	if len(res.Selections) == 0 {
		t.Error("expected global selection in mismatched repo")
	}
}

func TestRouterLatencyBudgetTrunc(t *testing.T) {
	docs := buildCorpus()
	calls := 0
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := NewRouter(RouterConfig{
		LatencyBudget: time.Millisecond,
		Now: func() time.Time {
			calls++
			// Advance time 2ms after the first call so the deadline trips.
			return now.Add(time.Duration(calls) * 2 * time.Millisecond)
		},
	})
	res := r.Select(docs, SelectionSignals{Keywords: []string{"go"}})
	if !res.Truncated {
		t.Errorf("expected truncation under tight latency budget; got %+v", res)
	}
}

func TestSelectionAsAnalysisEvents(t *testing.T) {
	docs := buildCorpus()
	r := NewRouter(RouterConfig{IncludeGlobalScope: true})
	res := r.Select(docs, SelectionSignals{Keywords: []string{"rules"}})
	now := time.Now().UTC()
	events := res.AsAnalysisEvents(now)
	if len(events) != len(res.Selections) {
		t.Errorf("events = %d, want %d", len(events), len(res.Selections))
	}
	for _, ev := range events {
		if ev.Exposures != 1 {
			t.Errorf("exposures = %d, want 1", ev.Exposures)
		}
		if ev.ROIScore <= 0 {
			t.Errorf("ROIScore = %f, want > 0", ev.ROIScore)
		}
	}
}
