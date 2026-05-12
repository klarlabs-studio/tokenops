package rules

import (
	"sort"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/tokenizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// AnalysisOptions tunes the analyzer.
type AnalysisOptions struct {
	// Providers lists the tokenizers to measure each document against. When
	// empty the analyzer falls back to the OpenAI tokenizer (the most
	// commonly assumed counter for context-window planning).
	Providers []eventschema.Provider
	// Registry resolves Providers to tokenizer implementations. When nil a
	// fresh tokenizer.NewRegistry() is used.
	Registry *tokenizer.Registry
	// Now returns the timestamp stamped onto events that lack a source
	// ModTime. Defaults to time.Now.
	Now func() time.Time
}

// SectionSummary is a per-section roll-up used by the analyzer to surface
// the biggest contributors, density metrics, and duplicate-hash candidates
// for downstream conflict and dedupe passes.
type SectionSummary struct {
	SourceID   string
	SectionID  string
	Anchor     string
	TokenCount int64
	CharCount  int64
	// TokensPerKChar normalises token cost across tokenizers: tokens per
	// 1000 characters. High values flag dense, expensive content.
	TokensPerKChar float64
	Hash           string
	// Provider identifies the tokenizer measurement that produced this row.
	Provider eventschema.Provider
}

// DocumentSummary is a per-document roll-up surfaced by the analyzer.
type DocumentSummary struct {
	SourceID    string
	Source      eventschema.RuleSource
	Scope       eventschema.RuleScope
	Path        string
	RepoID      string
	Provider    eventschema.Provider
	TotalTokens int64
	TotalChars  int64
	Sections    int
	Hash        string
	// TopSections lists the largest sections by token count, descending.
	TopSections []SectionSummary
}

// AnalysisResult bundles per-provider measurements and the underlying
// RuleSourceEvents so callers can persist, render, or forward them.
type AnalysisResult struct {
	// Events is the flat list of RuleSourceEvents (one per document per
	// provider). Order is stable: documents in path order, providers in
	// option order.
	Events []*eventschema.RuleSourceEvent
	// Documents groups summaries by SourceID for CLI rendering.
	Documents []DocumentSummary
	// DuplicateGroups buckets section IDs that share a body hash across
	// (or within) documents. Each bucket carries the SourceID:SectionID
	// members. Used by the conflict / dedupe pass that comes next.
	DuplicateGroups map[string][]string
}

// Analyzer computes per-rule measurements and summaries.
type Analyzer struct {
	opts AnalysisOptions
}

// NewAnalyzer returns an Analyzer with the given options.
func NewAnalyzer(opts AnalysisOptions) *Analyzer {
	if opts.Registry == nil {
		opts.Registry = tokenizer.NewRegistry()
	}
	if len(opts.Providers) == 0 {
		opts.Providers = []eventschema.Provider{eventschema.ProviderOpenAI}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Analyzer{opts: opts}
}

// Analyze produces an AnalysisResult covering every document in docs.
func (a *Analyzer) Analyze(docs []*RuleDocument) (*AnalysisResult, error) {
	out := &AnalysisResult{
		DuplicateGroups: map[string][]string{},
	}
	now := a.opts.Now()
	hashIndex := map[string][]string{}

	for _, doc := range docs {
		for _, p := range a.opts.Providers {
			tok, err := a.opts.Registry.Lookup(p)
			if err != nil {
				return nil, err
			}
			ev := ToSourceEvent(doc, tok, now)
			out.Events = append(out.Events, ev)
			out.Documents = append(out.Documents, summarize(doc, ev, p))
		}
		// Hash buckets are tokenizer-independent, so build them once per
		// document using the section bodies.
		for _, b := range doc.Blocks {
			h := b.Hash()
			id := b.ID(doc.SourceID)
			hashIndex[h] = append(hashIndex[h], id)
		}
	}

	for h, ids := range hashIndex {
		if len(ids) < 2 {
			continue
		}
		out.DuplicateGroups[h] = ids
	}

	return out, nil
}

// AnalyzeDocs is the canonical entry — adapters load the corpus via
// the rulesfs infrastructure adapter and pass the materialised
// documents in. Keeping the domain free of filesystem types means
// AnalyzeDocs is pure and unit-testable without any io/fs detour.
func AnalyzeDocs(docs []*RuleDocument, opts AnalysisOptions) (*AnalysisResult, error) {
	return NewAnalyzer(opts).Analyze(docs)
}

func summarize(doc *RuleDocument, ev *eventschema.RuleSourceEvent, p eventschema.Provider) DocumentSummary {
	sec := make([]SectionSummary, 0, len(ev.Sections))
	for _, s := range ev.Sections {
		var ratio float64
		if s.CharCount > 0 {
			ratio = float64(s.TokenCount) * 1000.0 / float64(s.CharCount)
		}
		sec = append(sec, SectionSummary{
			SourceID:       doc.SourceID,
			SectionID:      s.ID,
			Anchor:         s.Anchor,
			TokenCount:     s.TokenCount,
			CharCount:      s.CharCount,
			TokensPerKChar: ratio,
			Hash:           s.Hash,
			Provider:       p,
		})
	}
	sort.SliceStable(sec, func(i, j int) bool { return sec[i].TokenCount > sec[j].TokenCount })
	top := sec
	if len(top) > 5 {
		top = top[:5]
	}
	return DocumentSummary{
		SourceID:    doc.SourceID,
		Source:      doc.Source,
		Scope:       doc.Scope,
		Path:        doc.Path,
		RepoID:      doc.RepoID,
		Provider:    p,
		TotalTokens: ev.TotalTokens,
		TotalChars:  ev.TotalChars,
		Sections:    len(ev.Sections),
		Hash:        ev.Hash,
		TopSections: top,
	}
}
