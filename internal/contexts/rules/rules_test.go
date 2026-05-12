package rules

import (
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/tokenizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func TestClassifySource(t *testing.T) {
	cases := map[string]eventschema.RuleSource{
		"CLAUDE.md":                eventschema.RuleSourceClaudeMD,
		"AGENTS.md":                eventschema.RuleSourceAgentsMD,
		"docs/AGENTS.md":           eventschema.RuleSourceAgentsMD,
		".cursor/rules/go.mdc":     eventschema.RuleSourceCursorRules,
		"server.mcp.yaml":          eventschema.RuleSourceMCPPolicy,
		"server.mcp.json":          eventschema.RuleSourceMCPPolicy,
		"docs/conventions/test.md": eventschema.RuleSourceRepoConvention,
		"README.md":                eventschema.RuleSourceRepoConvention,
	}
	for path, want := range cases {
		t.Run(path, func(t *testing.T) {
			if got := ClassifySource(path); got != want {
				t.Errorf("ClassifySource(%q) = %q, want %q", path, got, want)
			}
		})
	}
}

func TestParseMarkdownHeadingHierarchy(t *testing.T) {
	body := strings.Join([]string{
		"preamble line",
		"",
		"# Top",
		"top body",
		"",
		"## Testing",
		"testing body",
		"",
		"### TDD",
		"tdd body",
		"",
		"## Style",
		"style body",
	}, "\n")
	blocks := ParseMarkdown(body)
	anchors := make([]string, 0, len(blocks))
	for _, b := range blocks {
		anchors = append(anchors, b.Anchor)
	}
	want := []string{"", "Top", "Top/Testing", "Top/Testing/TDD", "Top/Style"}
	if len(anchors) != len(want) {
		t.Fatalf("got %v anchors, want %v", anchors, want)
	}
	for i, w := range want {
		if anchors[i] != w {
			t.Errorf("anchor[%d] = %q, want %q", i, anchors[i], w)
		}
	}
	// Preamble must retain its body.
	if !strings.Contains(blocks[0].Body, "preamble line") {
		t.Errorf("preamble body lost: %q", blocks[0].Body)
	}
}

func TestParseMarkdownIgnoresFencedHeadings(t *testing.T) {
	body := strings.Join([]string{
		"# Real",
		"```",
		"# not a heading",
		"```",
		"after",
	}, "\n")
	blocks := ParseMarkdown(body)
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2; anchors=%v", len(blocks), anchorsOf(blocks))
	}
	if !strings.Contains(blocks[1].Body, "# not a heading") {
		t.Errorf("fenced heading was split: %q", blocks[1].Body)
	}
}

func TestToSourceEventCarriesSections(t *testing.T) {
	doc := &RuleDocument{
		SourceID: "repo:CLAUDE.md",
		Source:   eventschema.RuleSourceClaudeMD,
		Scope:    eventschema.RuleScopeRepo,
		Path:     "CLAUDE.md",
		RepoID:   "repo",
		Body:     "# Top\nbody\n## Sub\nmore\n",
		ModTime:  time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC),
	}
	doc.Blocks = ParseMarkdown(doc.Body)
	tok := tokenizer.NewOpenAITokenizer()
	ev := ToSourceEvent(doc, tok, time.Now())
	if ev == nil {
		t.Fatal("expected non-nil event")
	}
	if ev.SourceID != doc.SourceID {
		t.Errorf("SourceID = %q, want %q", ev.SourceID, doc.SourceID)
	}
	if ev.Provider != tok.Provider() {
		t.Errorf("provider = %q, want %q", ev.Provider, tok.Provider())
	}
	if ev.TotalTokens <= 0 {
		t.Errorf("TotalTokens = %d, want > 0", ev.TotalTokens)
	}
	if len(ev.Sections) != len(doc.Blocks) {
		t.Errorf("sections = %d, want %d", len(ev.Sections), len(doc.Blocks))
	}
	for _, s := range ev.Sections {
		if !strings.HasPrefix(s.Hash, "sha256:") {
			t.Errorf("section hash = %q, want sha256: prefix", s.Hash)
		}
	}
	if !strings.HasPrefix(ev.Hash, "sha256:") {
		t.Errorf("doc hash = %q, want sha256: prefix", ev.Hash)
	}
	if !ev.IngestedAt.Equal(doc.ModTime) {
		t.Errorf("IngestedAt = %v, want %v", ev.IngestedAt, doc.ModTime)
	}
}

func anchorsOf(bs []RuleBlock) []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.Anchor)
	}
	return out
}
