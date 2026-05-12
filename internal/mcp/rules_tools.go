package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/rules"
	"github.com/felixgeelhaar/tokenops/internal/infra/rulesfs"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// --- input structs --------------------------------------------------------

type rulesAnalyzeInput struct {
	Root     string `json:"root,omitempty"`
	RepoID   string `json:"repo_id,omitempty"`
	Provider string `json:"provider,omitempty" jsonschema:"enum=openai,enum=anthropic,enum=gemini"`
}

type rulesConflictsInput struct {
	Root   string `json:"root,omitempty"`
	RepoID string `json:"repo_id,omitempty"`
}

type rulesCompressInput struct {
	Root                string  `json:"root,omitempty"`
	RepoID              string  `json:"repo_id,omitempty"`
	SimilarityThreshold float64 `json:"similarity_threshold,omitempty"`
	QualityFloor        float64 `json:"quality_floor,omitempty"`
}

type rulesInjectInput struct {
	Root          string   `json:"root,omitempty"`
	RepoID        string   `json:"repo_id,omitempty"`
	WorkflowID    string   `json:"workflow_id,omitempty"`
	AgentID       string   `json:"agent_id,omitempty"`
	Files         []string `json:"files,omitempty"`
	Tools         []string `json:"tools,omitempty"`
	Keywords      []string `json:"keywords,omitempty"`
	TokenBudget   int64    `json:"token_budget,omitempty"`
	MinScore      float64  `json:"min_score,omitempty"`
	IncludeGlobal bool     `json:"include_global,omitempty"`
}

// RegisterRulesTools attaches the Rule Intelligence MCP tool surface
// (analyze, conflicts, compress, inject) to s. Read-only.
//
// The MCP layer reuses the same Ingestor + Router + Compressor
// implementations the CLI calls, so behavior stays consistent across
// surfaces. Raw rule body content is never returned — only metrics,
// hashes, anchors, and routing rationale — keeping redaction structural.
func RegisterRulesTools(s *Server) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_rules_analyze").
		Description("Measure token cost of operational rule artifacts (CLAUDE.md, AGENTS.md, Cursor rules, MCP policies). Returns per-document totals and per-section breakdowns plus tokenizer-independent duplicate groups.").
		Handler(func(_ context.Context, in rulesAnalyzeInput) (string, error) {
			return rulesAnalyze(in)
		})
	s.Tool("tokenops_rules_conflicts").
		Description("Detect redundancy, drift, and anti-pattern conflicts across rule artifacts. Returns Finding records without raw body text.").
		Handler(func(_ context.Context, in rulesConflictsInput) (string, error) {
			return rulesConflicts(in)
		})
	s.Tool("tokenops_rules_compress").
		Description("Distill the rule corpus by dropping redundant and near-duplicate sections. Returns per-document token totals before/after, quality score, and whether the result is accepted under the quality floor.").
		Handler(func(_ context.Context, in rulesCompressInput) (string, error) {
			return rulesCompress(in)
		})
	s.Tool("tokenops_rules_inject").
		Description("Preview the dynamic rule subset the router selects for a request context (workflow, agent, files, tools, keywords). Returns selections with rationale and budget metrics.").
		Handler(func(_ context.Context, in rulesInjectInput) (string, error) {
			return rulesInject(in)
		})
	return nil
}

func rulesAnalyze(in rulesAnalyzeInput) (string, error) {
	prov := eventschema.ProviderOpenAI
	switch in.Provider {
	case "", "openai":
	case "anthropic":
		prov = eventschema.ProviderAnthropic
	case "gemini":
		prov = eventschema.ProviderGemini
	default:
		return "", fmt.Errorf("unknown provider %q", in.Provider)
	}
	docs, err := rulesfs.LoadCorpus(in.Root, in.RepoID)
	if err != nil {
		return "", err
	}
	res, err := rules.AnalyzeDocs(docs, rules.AnalysisOptions{
		Providers: []eventschema.Provider{prov},
	})
	if err != nil {
		return "", err
	}
	return jsonString(struct {
		Documents       []rules.DocumentSummary `json:"documents"`
		DuplicateGroups map[string][]string     `json:"duplicate_groups,omitempty"`
	}{Documents: res.Documents, DuplicateGroups: res.DuplicateGroups}), nil
}

func rulesConflicts(in rulesConflictsInput) (string, error) {
	docs, err := rulesfs.LoadCorpus(in.Root, in.RepoID)
	if err != nil {
		return "", err
	}
	findings := rules.DetectConflicts(docs, rules.ConflictOptions{})
	return jsonString(struct {
		Findings []rules.Finding `json:"findings"`
	}{Findings: findings}), nil
}

func rulesCompress(in rulesCompressInput) (string, error) {
	docs, err := rulesfs.LoadCorpus(in.Root, in.RepoID)
	if err != nil {
		return "", err
	}
	c := rules.NewCompressor(rules.CompressConfig{
		SimilarityThreshold: in.SimilarityThreshold,
		QualityFloor:        in.QualityFloor,
	}, nil)
	type view struct {
		SourceID         string  `json:"source_id"`
		Path             string  `json:"path"`
		OriginalTokens   int64   `json:"original_tokens"`
		CompressedTokens int64   `json:"compressed_tokens"`
		QualityScore     float64 `json:"quality_score"`
		Accepted         bool    `json:"accepted"`
		DroppedSections  int     `json:"dropped_sections"`
	}
	views := make([]view, 0, len(docs))
	for _, d := range docs {
		r := c.Compress(d)
		dropped := 0
		for _, s := range r.Sections {
			if s.Dropped {
				dropped++
			}
		}
		views = append(views, view{
			SourceID:         r.SourceID,
			Path:             d.Path,
			OriginalTokens:   r.OriginalTokens,
			CompressedTokens: r.CompressedTokens,
			QualityScore:     r.QualityScore,
			Accepted:         r.Accepted,
			DroppedSections:  dropped,
		})
	}
	return jsonString(struct {
		Results []view `json:"results"`
	}{Results: views}), nil
}

func rulesInject(in rulesInjectInput) (string, error) {
	docs, err := rulesfs.LoadCorpus(in.Root, in.RepoID)
	if err != nil {
		return "", err
	}
	r := rules.NewRouter(rules.RouterConfig{
		TokenBudget:        in.TokenBudget,
		MinScore:           in.MinScore,
		IncludeGlobalScope: in.IncludeGlobal,
		LatencyBudget:      200 * time.Millisecond,
	})
	res := r.Select(docs, rules.SelectionSignals{
		WorkflowID: in.WorkflowID,
		AgentID:    in.AgentID,
		RepoID:     in.RepoID,
		FilePaths:  in.Files,
		Tools:      in.Tools,
		Keywords:   in.Keywords,
	})
	return jsonString(res), nil
}
