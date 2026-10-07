// Package ruleintel answers the Rule Intelligence questions about a rule
// corpus on disk (CLAUDE.md, AGENTS.md, Cursor rules): what it weighs,
// where it contradicts itself, what compressing it would save, and which
// sections a given piece of work should see. The daemon's /api/rules/*
// routes, the tokenops_rules_* tools and the `tokenops rules` commands
// all answer from it (ADR 0010).
//
// Every answer re-reads the corpus, so a rule edit shows up on the next
// call. Answers carry metrics, anchors, hashes and routing rationale,
// never rule bodies, so redaction is structural. The one exception is
// CompressDetailed, which the local CLI asks for explicitly to print the
// compacted corpus.
package ruleintel

import (
	"time"

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
	docs, err := rulesfs.LoadCorpus(c.Root, c.RepoID)
	if err != nil {
		return nil, &CorpusError{Err: err}
	}
	return docs, nil
}

// CorpusError means the rule corpus could not be read: a root that is not
// there or not readable, which is the caller's to correct rather than a
// failure of the analysis. Its message is the underlying error's.
type CorpusError struct{ Err error }

func (e *CorpusError) Error() string { return e.Err.Error() }

// Unwrap returns the read error.
func (e *CorpusError) Unwrap() error { return e.Err }

// The rules domain's answer types, aliased so it stays their single
// definition.
type (
	// DocumentSummary is what one rule document weighs.
	DocumentSummary = rules.DocumentSummary
	// Finding is one conflict in the corpus.
	Finding = rules.Finding
	// CompressedSection is what compression did to one section.
	CompressedSection = rules.CompressedSection
	// BenchmarkResult is a rule benchmark's scoreboard.
	BenchmarkResult = rules.BenchmarkResult
)

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

// ParseProvider is Provider for callers that refuse a name it does not
// know: it reports false for anything but "", openai, anthropic and
// gemini.
func ParseProvider(name string) (eventschema.Provider, bool) {
	switch name {
	case "", "openai", "anthropic", "gemini":
		return Provider(name), true
	default:
		return "", false
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
	Findings []Finding `json:"findings"`
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
	detail, err := CompressDetailed(c, opts, false)
	if err != nil {
		return Compression{}, err
	}
	out := make([]CompressedDocument, 0, len(detail))
	for _, d := range detail {
		out = append(out, d.CompressedDocument)
	}
	return Compression{Results: out}, nil
}

// CompressedDetail is CompressedDocument with what compression did to
// each section and, when asked for, the compacted body. It carries rule
// text, so it is for a local reader, never an API answer.
type CompressedDetail struct {
	CompressedDocument
	Sections []CompressedSection
	Body     string
}

// CompressDetailed is Compress with each document's sections and, when
// withBody is set, its compacted body, in corpus order.
func CompressDetailed(c Corpus, opts CompressOptions, withBody bool) ([]CompressedDetail, error) {
	docs, err := c.load()
	if err != nil {
		return nil, err
	}
	comp := rules.NewCompressor(rules.CompressConfig{
		SimilarityThreshold: opts.SimilarityThreshold,
		QualityFloor:        opts.QualityFloor,
	}, nil)
	out := make([]CompressedDetail, 0, len(docs))
	for _, d := range docs {
		r := comp.Compress(d)
		dropped := 0
		for _, s := range r.Sections {
			if s.Dropped {
				dropped++
			}
		}
		detail := CompressedDetail{
			CompressedDocument: CompressedDocument{
				SourceID:         r.SourceID,
				Path:             d.Path,
				OriginalTokens:   r.OriginalTokens,
				CompressedTokens: r.CompressedTokens,
				QualityScore:     r.QualityScore,
				Accepted:         r.Accepted,
				DroppedSections:  dropped,
			},
			Sections: r.Sections,
		}
		if withBody {
			detail.Body = r.CompactedBody()
		}
		out = append(out, detail)
	}
	return out, nil
}

// InjectQuery is the work in hand and the selection budget.
type InjectQuery struct {
	MinScore           float64
	TokenBudget        int64
	IncludeGlobalScope bool
	// LatencyBudget bounds the selection's wall-clock time; zero is
	// unbounded.
	LatencyBudget time.Duration

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
		LatencyBudget:      q.LatencyBudget,
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

// Bench runs the rule benchmark spec (YAML or JSON) and scores each
// profile against each scenario, reading every profile's corpus from
// disk.
func Bench(spec []byte) (*BenchmarkResult, error) {
	parsed, err := rules.ParseBenchSpec(spec)
	if err != nil {
		return nil, err
	}
	return rules.RunBenchSpec(parsed, rulesfs.LoadCorpus)
}
