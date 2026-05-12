package rules

import (
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/tokenizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// TokenizerLabel returns the human-readable identifier carried in
// RuleSourceEvent.Tokenizer to disambiguate measurements taken under
// different tokenizers for the same artifact.
func TokenizerLabel(p eventschema.Provider) string {
	switch p {
	case eventschema.ProviderOpenAI:
		return "openai/cl100k_base"
	case eventschema.ProviderAnthropic:
		return "anthropic"
	case eventschema.ProviderGemini:
		return "gemini"
	default:
		return string(p)
	}
}

// ToSourceEvent renders a RuleDocument as a RuleSourceEvent measured under
// the supplied tokenizer. now stamps IngestedAt when the document carries no
// modification time.
func ToSourceEvent(doc *RuleDocument, tok tokenizer.Tokenizer, now time.Time) *eventschema.RuleSourceEvent {
	if doc == nil {
		return nil
	}
	totalTokens := int64(tok.CountText(doc.Body))
	sections := make([]eventschema.RuleSection, 0, len(doc.Blocks))
	for _, b := range doc.Blocks {
		sections = append(sections, eventschema.RuleSection{
			ID:         b.ID(doc.SourceID),
			Anchor:     b.Anchor,
			TokenCount: int64(tok.CountText(b.Body)),
			CharCount:  b.CharCount(),
			Hash:       b.Hash(),
		})
	}
	ingested := doc.ModTime
	if ingested.IsZero() {
		ingested = now
	}
	return &eventschema.RuleSourceEvent{
		SourceID:    doc.SourceID,
		Source:      doc.Source,
		Scope:       doc.Scope,
		Path:        doc.Path,
		RepoID:      doc.RepoID,
		Tokenizer:   TokenizerLabel(tok.Provider()),
		Provider:    tok.Provider(),
		TotalTokens: totalTokens,
		TotalChars:  doc.CharCount(),
		Hash:        doc.Hash(),
		Sections:    sections,
		IngestedAt:  ingested,
	}
}
