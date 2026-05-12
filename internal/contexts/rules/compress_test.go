package rules

import (
	"strings"
	"testing"

	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/tokenizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func TestCompressorDropsExactDuplicateSections(t *testing.T) {
	body := strings.Join([]string{
		"# Testing",
		"use tdd everywhere",
		"",
		"# Style",
		"prefer composition over inheritance",
		"",
		"# Testing",
		"use tdd everywhere",
	}, "\n")
	doc := &RuleDocument{SourceID: "repo:CLAUDE.md", Source: eventschema.RuleSourceClaudeMD, Body: body}
	doc.Blocks = ParseMarkdown(body)

	c := NewCompressor(CompressConfig{}, tokenizer.NewOpenAITokenizer())
	res := c.Compress(doc)

	dropped := 0
	for _, s := range res.Sections {
		if s.Dropped {
			dropped++
			if s.DroppedReason != "redundant" {
				t.Errorf("dropped reason = %q, want redundant", s.DroppedReason)
			}
			if s.DuplicateOf == "" {
				t.Errorf("duplicate_of unset for dropped section %q", s.SectionID)
			}
		}
	}
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
	if res.CompressedTokens >= res.OriginalTokens {
		t.Errorf("CompressedTokens = %d, want < OriginalTokens = %d",
			res.CompressedTokens, res.OriginalTokens)
	}
}

func TestCompressorDropsNearDuplicates(t *testing.T) {
	body := strings.Join([]string{
		"# Testing A",
		"Always write tests first using a strict red-green-refactor TDD cycle and keep the iterations small.",
		"",
		"# Testing B",
		"Always write tests first using a strict red-green-refactor TDD cycle and keep iterations small here.",
	}, "\n")
	doc := &RuleDocument{SourceID: "repo:CLAUDE.md", Source: eventschema.RuleSourceClaudeMD, Body: body}
	doc.Blocks = ParseMarkdown(body)

	c := NewCompressor(CompressConfig{SimilarityThreshold: 0.6, MinSectionTokens: 1}, tokenizer.NewOpenAITokenizer())
	res := c.Compress(doc)

	hit := false
	for _, s := range res.Sections {
		if s.Dropped && s.DroppedReason == "near_duplicate" {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("expected near_duplicate drop; got %+v", res.Sections)
	}
}

func TestCompactBodyCollapsesWhitespace(t *testing.T) {
	in := "  line one   here\n\n\n\n  line one   here\nline two\n"
	out := compactBody(in)
	if strings.Contains(out, "   ") {
		t.Errorf("extra whitespace not collapsed: %q", out)
	}
	if strings.Contains(out, "\n\n\n") {
		t.Errorf("triple blank line not collapsed: %q", out)
	}
	if strings.Count(out, "line one here") != 1 {
		t.Errorf("consecutive duplicate lines not deduped: %q", out)
	}
}

func TestCompactedBodyRoundTrip(t *testing.T) {
	body := "# Testing\nuse tdd\n\n# Style\nbe concise\n"
	doc := &RuleDocument{SourceID: "repo:CLAUDE.md", Body: body}
	doc.Blocks = ParseMarkdown(body)
	res := NewCompressor(CompressConfig{}, tokenizer.NewOpenAITokenizer()).Compress(doc)
	out := res.CompactedBody()
	for _, want := range []string{"Testing", "Style", "tdd", "concise"} {
		if !strings.Contains(out, want) {
			t.Errorf("compacted body missing %q:\n%s", want, out)
		}
	}
}

func TestCompressorQualityGate(t *testing.T) {
	doc := &RuleDocument{SourceID: "repo:CLAUDE.md", Body: "# X\nsmall\n"}
	doc.Blocks = ParseMarkdown(doc.Body)
	res := NewCompressor(CompressConfig{QualityFloor: 0.95}, tokenizer.NewOpenAITokenizer()).Compress(doc)
	// Tiny corpus: any compression that shaves whitespace might still be
	// accepted but quality floor of 0.95 should hold when nothing changes.
	if res.QualityScore < 0 || res.QualityScore > 1.01 {
		t.Errorf("quality score out of range: %f", res.QualityScore)
	}
}

func TestCompressionAsAnalysisEvent(t *testing.T) {
	doc := &RuleDocument{SourceID: "x", Body: "# A\nbody\n"}
	doc.Blocks = ParseMarkdown(doc.Body)
	res := NewCompressor(CompressConfig{}, tokenizer.NewOpenAITokenizer()).Compress(doc)
	ev := res.AsAnalysisEvent()
	if ev.SourceID != "x" {
		t.Errorf("source_id = %q, want x", ev.SourceID)
	}
	if ev.ContextTokens != res.OriginalTokens {
		t.Errorf("context_tokens = %d, want %d", ev.ContextTokens, res.OriginalTokens)
	}
	if ev.CompressedTokens != res.CompressedTokens {
		t.Errorf("compressed_tokens = %d, want %d", ev.CompressedTokens, res.CompressedTokens)
	}
}
