// Package ruleintel answers the Rule Intelligence questions about a rule
// corpus on disk (CLAUDE.md, AGENTS.md, Cursor rules): what it weighs,
// where it contradicts itself, what compressing it would save, and which
// sections a given piece of work should see. The daemon's /api/rules/*
// routes serve it (ADR 0010).
//
// Every answer re-reads the corpus, so a rule edit shows up on the next
// call. Answers carry metrics, anchors, hashes and routing rationale,
// never rule bodies, so redaction is structural.
package ruleintel

import (
	"go.klarlabs.de/tokenops/internal/contexts/rules"
	"go.klarlabs.de/tokenops/internal/infra/rulesfs"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Corpus names the rule sources to read: the directory to scan and the
// repository the documents belong to.
type Corpus struct {
	Root, RepoID string
}

func (c Corpus) load() ([]*rules.RuleDocument, error) {
	return rulesfs.LoadCorpus(c.Root, c.RepoID)
}

// Provider is the tokenizer family a caller names: anthropic, gemini, or
// openai for anything else, including nothing.
func Provider(name string) eventschema.Provider {
	switch name {
	case "anthropic":
		return eventschema.ProviderAnthropic
	case "gemini":
		return eventschema.ProviderGemini
	default:
		return eventschema.ProviderOpenAI
	}
}

// Analysis is what each document weighs under one provider's tokenizer,
// and which sections repeat.
type Analysis struct {
	Documents       []rules.DocumentSummary `json:"documents"`
	DuplicateGroups map[string][]string     `json:"duplicate_groups"`
}

// Analyze measures the corpus with prov's tokenizer.
func Analyze(c Corpus, prov eventschema.Provider) (Analysis, error) {
	docs, err := c.load()
	if err != nil {
		return Analysis{}, err
	}
	res, err := rules.AnalyzeDocs(docs, rules.AnalysisOptions{
		Providers: []eventschema.Provider{prov},
	})
	if err != nil {
		return Analysis{}, err
	}
	return Analysis{Documents: res.Documents, DuplicateGroups: res.DuplicateGroups}, nil
}

// Conflicts are the places the corpus contradicts or repeats itself.
type Conflicts struct {
	Findings []rules.Finding `json:"findings"`
}

// DetectConflicts reads the corpus and reports its conflicts.
func DetectConflicts(c Corpus) (Conflicts, error) {
	docs, err := c.load()
	if err != nil {
		return Conflicts{}, err
	}
	return Conflicts{Findings: rules.DetectConflicts(docs, rules.ConflictOptions{})}, nil
}

// CompressOptions tunes compression; zero values take the compressor's
// defaults.
type CompressOptions struct {
	SimilarityThreshold float64
	QualityFloor        float64
}

// CompressedDocument is what compressing one document would do.
type CompressedDocument struct {
	SourceID         string  `json:"source_id"`
	Path             string  `json:"path"`
	OriginalTokens   int64   `json:"original_tokens"`
	CompressedTokens int64   `json:"compressed_tokens"`
	QualityScore     float64 `json:"quality_score"`
	Accepted         bool    `json:"accepted"`
	DroppedSections  int     `json:"dropped_sections"`
}

// Compression is the per-document result, in corpus order.
type Compression struct {
	Results []CompressedDocument `json:"results"`
}

// Compress reports what compressing each document would save.
func Compress(c Corpus, opts CompressOptions) (Compression, error) {
	docs, err := c.load()
	if err != nil {
		return Compression{}, err
	}
	comp := rules.NewCompressor(rules.CompressConfig{
		SimilarityThreshold: opts.SimilarityThreshold,
		QualityFloor:        opts.QualityFloor,
	}, nil)
	out := make([]CompressedDocument, 0, len(docs))
	for _, d := range docs {
		r := comp.Compress(d)
		dropped := 0
		for _, s := range r.Sections {
			if s.Dropped {
				dropped++
			}
		}
		out = append(out, CompressedDocument{
			SourceID:         r.SourceID,
			Path:             d.Path,
			OriginalTokens:   r.OriginalTokens,
			CompressedTokens: r.CompressedTokens,
			QualityScore:     r.QualityScore,
			Accepted:         r.Accepted,
			DroppedSections:  dropped,
		})
	}
	return Compression{Results: out}, nil
}

// InjectQuery is the work in hand and the selection budget.
type InjectQuery struct {
	MinScore           float64
	TokenBudget        int64
	IncludeGlobalScope bool

	WorkflowID string
	AgentID    string
	FilePaths  []string
	Tools      []string
	Keywords   []string
}

// Selection is the sections chosen for the work and why.
type Selection = rules.SelectionResult

// Inject selects the sections the described work should see. The
// corpus's repository is the selection's repository.
func Inject(c Corpus, q InjectQuery) (*Selection, error) {
	docs, err := c.load()
	if err != nil {
		return nil, err
	}
	router := rules.NewRouter(rules.RouterConfig{
		MinScore:           q.MinScore,
		TokenBudget:        q.TokenBudget,
		IncludeGlobalScope: q.IncludeGlobalScope,
	})
	return router.Select(docs, rules.SelectionSignals{
		WorkflowID: q.WorkflowID,
		AgentID:    q.AgentID,
		RepoID:     c.RepoID,
		FilePaths:  q.FilePaths,
		Tools:      q.Tools,
		Keywords:   q.Keywords,
	}), nil
}
