package rules

import (
	"slices"
	"strings"
	"testing"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func docFromBody(sourceID, path string, src eventschema.RuleSource, body string) *RuleDocument {
	d := &RuleDocument{
		SourceID: sourceID,
		Source:   src,
		Scope:    eventschema.RuleScopeRepo,
		Path:     path,
		Body:     body,
	}
	d.Blocks = ParseMarkdown(body)
	return d
}

func TestDetectConflictsRedundant(t *testing.T) {
	bodyA := "# Testing\nuse tdd everywhere\n"
	bodyB := "# Testing\nuse tdd everywhere\n"
	docs := []*RuleDocument{
		docFromBody("repo:A.md", "A.md", eventschema.RuleSourceClaudeMD, bodyA),
		docFromBody("repo:B.md", "B.md", eventschema.RuleSourceAgentsMD, bodyB),
	}
	got := DetectConflicts(docs, ConflictOptions{})
	found := false
	for _, f := range got {
		if f.Kind == FindingRedundant && len(f.Members) >= 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected redundant finding, got %+v", got)
	}
}

func TestDetectConflictsDrift(t *testing.T) {
	docs := []*RuleDocument{
		docFromBody("repo:CLAUDE.md", "CLAUDE.md", eventschema.RuleSourceClaudeMD,
			"# Testing\nuse tdd everywhere\n"),
		docFromBody("repo:AGENTS.md", "AGENTS.md", eventschema.RuleSourceAgentsMD,
			"# Testing\nwrite tests after implementation\n"),
	}
	got := DetectConflicts(docs, ConflictOptions{})
	var hit *Finding
	for i := range got {
		if got[i].Kind == FindingDrift {
			hit = &got[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("expected drift finding, got %+v", got)
	}
	if hit.Anchor != "Testing" {
		t.Errorf("drift anchor = %q, want %q", hit.Anchor, "Testing")
	}
	if len(hit.Members) < 2 {
		t.Errorf("drift members = %v, want at least 2", hit.Members)
	}
}

func TestDetectConflictsAntiPatternCrossSection(t *testing.T) {
	docs := []*RuleDocument{
		docFromBody("repo:CLAUDE.md", "CLAUDE.md", eventschema.RuleSourceClaudeMD,
			"# Tone\nBe concise in all responses.\n"),
		docFromBody("repo:AGENTS.md", "AGENTS.md", eventschema.RuleSourceAgentsMD,
			"# Tone\nExplain thoroughly with detailed reasoning.\n"),
	}
	got := DetectConflicts(docs, ConflictOptions{})
	var hit *Finding
	for i := range got {
		if got[i].Kind == FindingAntiPattern {
			hit = &got[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("expected anti_pattern finding, got %+v", got)
	}
	if !contains(hit.Triggers, "concise_vs_verbose") {
		t.Errorf("triggers = %v, want concise_vs_verbose", hit.Triggers)
	}
}

func TestDetectConflictsAntiPatternWithinSection(t *testing.T) {
	docs := []*RuleDocument{
		docFromBody("repo:CLAUDE.md", "CLAUDE.md", eventschema.RuleSourceClaudeMD,
			"# Tone\nBe concise. Also: explain thoroughly with detailed reasoning.\n"),
	}
	got := DetectConflicts(docs, ConflictOptions{})
	found := false
	for _, f := range got {
		if f.Kind == FindingAntiPattern && len(f.Members) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected within-section anti_pattern, got %+v", got)
	}
}

func TestDetectConflictsBodyNotLeaked(t *testing.T) {
	secret := "TOP_SECRET_PROD_PASSWORD_LITERAL"
	docs := []*RuleDocument{
		docFromBody("repo:A.md", "A.md", eventschema.RuleSourceClaudeMD,
			"# Testing\n"+secret+" use tdd\n"),
		docFromBody("repo:B.md", "B.md", eventschema.RuleSourceAgentsMD,
			"# Testing\n"+secret+" use tdd\n"),
	}
	got := DetectConflicts(docs, ConflictOptions{})
	if len(got) == 0 {
		t.Fatal("expected findings")
	}
	for _, f := range got {
		if strings.Contains(f.Detail, secret) {
			t.Errorf("detail leaked body: %q", f.Detail)
		}
	}
}

func TestFindingAsAnalysisEvent(t *testing.T) {
	red := Finding{Kind: FindingRedundant, Members: []string{"a", "b"}}
	ev := red.AsAnalysisEvent("a")
	if ev.SourceID != "a" {
		t.Errorf("source_id = %q, want a", ev.SourceID)
	}
	if len(ev.RedundantWith) != 2 {
		t.Errorf("redundant_with = %v, want 2 entries", ev.RedundantWith)
	}
	drift := Finding{Kind: FindingDrift, Members: []string{"a", "b"}}
	ev = drift.AsAnalysisEvent("a")
	if len(ev.ConflictsWith) != 2 {
		t.Errorf("conflicts_with = %v, want 2 entries", ev.ConflictsWith)
	}
}

func contains(xs []string, s string) bool { return slices.Contains(xs, s) }
