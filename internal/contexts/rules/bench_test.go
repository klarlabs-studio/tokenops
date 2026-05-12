package rules

import (
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func leanDocs() []*RuleDocument {
	d := &RuleDocument{
		SourceID: "repo:CLAUDE-lean.md", Source: eventschema.RuleSourceClaudeMD,
		Scope: eventschema.RuleScopeRepo, RepoID: "repo", Path: "CLAUDE-lean.md",
		Body: "# Testing\nuse tdd\n",
	}
	d.Blocks = ParseMarkdown(d.Body)
	return []*RuleDocument{d}
}

func bloatedDocs() []*RuleDocument {
	d := &RuleDocument{
		SourceID: "repo:CLAUDE-bloat.md", Source: eventschema.RuleSourceClaudeMD,
		Scope: eventschema.RuleScopeRepo, RepoID: "repo", Path: "CLAUDE-bloat.md",
		Body: "# Testing\nuse tdd\n## Security\ninput validation everywhere\n## Style\nbe concise\n## Logs\nstructured logging\n",
	}
	d.Blocks = ParseMarkdown(d.Body)
	return []*RuleDocument{d}
}

func TestBenchmarkLeanWinsByROIWhenBudgetTight(t *testing.T) {
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	exposure := Exposure{
		Requests:             100,
		OutputTokens:         5000,
		BaselineOutputTokens: 6500,
		Retries:              3,
		WindowStart:          now.Add(-time.Hour),
		WindowEnd:            now,
	}
	profiles := []Profile{
		{
			Name:   "lean",
			Docs:   leanDocs(),
			Router: RouterConfig{MinScore: 0.0, IncludeGlobalScope: false},
		},
		{
			Name:   "bloat",
			Docs:   bloatedDocs(),
			Router: RouterConfig{MinScore: 0.0, IncludeGlobalScope: false},
		},
	}
	scenarios := []Scenario{
		{
			Name:     "tdd-task",
			Signals:  SelectionSignals{RepoID: "repo", Keywords: []string{"testing"}},
			Exposure: exposure,
		},
	}
	res := NewBenchmark(ROIConfig{}).Run(profiles, scenarios)
	if len(res.Scores) != 2 {
		t.Fatalf("scores = %d, want 2", len(res.Scores))
	}
	winner, ok := res.Winners["tdd-task"]
	if !ok {
		t.Fatal("no winner for tdd-task")
	}
	if winner != "lean" {
		t.Errorf("winner = %q, want lean (lower context tokens → higher ROI)", winner)
	}
}

func TestBenchmarkScoresSortedDescByROIWithinScenario(t *testing.T) {
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	profiles := []Profile{
		{Name: "b", Docs: bloatedDocs(), Router: RouterConfig{MinScore: 0.0}},
		{Name: "a", Docs: leanDocs(), Router: RouterConfig{MinScore: 0.0}},
	}
	scenarios := []Scenario{
		{
			Name:    "s1",
			Signals: SelectionSignals{RepoID: "repo", Keywords: []string{"testing"}},
			Exposure: Exposure{
				Requests:             50,
				OutputTokens:         2000,
				BaselineOutputTokens: 2500,
				WindowStart:          now.Add(-time.Hour),
				WindowEnd:            now,
			},
		},
	}
	res := NewBenchmark(ROIConfig{}).Run(profiles, scenarios)
	if res.Scores[0].ROIScore < res.Scores[1].ROIScore {
		t.Errorf("scores not sorted desc within scenario: %+v", res.Scores)
	}
}

func TestBenchmarkEmptyInputsReturnEmptyResult(t *testing.T) {
	res := NewBenchmark(ROIConfig{}).Run(nil, nil)
	if len(res.Scores) != 0 || len(res.Winners) != 0 {
		t.Errorf("expected empty result, got %+v", res)
	}
}
