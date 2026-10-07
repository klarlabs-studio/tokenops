package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/ruleintel"
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

// --- output structs --------------------------------------------------------

// rulesAnalyzeResult is the typed payload for tokenops_rules (view=analyze).
type rulesAnalyzeResult struct {
	Documents       []ruleintel.DocumentSummary `json:"documents"`
	DuplicateGroups map[string][]string         `json:"duplicate_groups,omitempty"`
}

// rulesConflictsResult is the typed payload for tokenops_rules (view=conflicts).
type rulesConflictsResult struct {
	Findings []ruleintel.Finding `json:"findings"`
}

// rulesCompressResult is the typed payload for tokenops_rules
// (view=compress), the capability's answer.
type rulesCompressResult = ruleintel.Compression

// RegisterRulesTools attaches the Rule Intelligence MCP tool surface
// (analyze, conflicts, compress, inject) to s. Read-only.
//
// Each tool answers from capability/ruleintel, as the CLI and the daemon's
// /api/rules/* routes do, so behavior stays consistent across surfaces. Raw rule body content is never returned — only metrics,
// hashes, anchors, and routing rationale — keeping redaction structural.
func RegisterRulesTools(s *Server) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_rules_analyze").
		Description("Measure token cost of operational rule artifacts (CLAUDE.md, AGENTS.md, Cursor rules, MCP policies). Returns per-document totals and per-section breakdowns plus tokenizer-independent duplicate groups.").
		OutputSchema(rulesAnalyzeResult{}).
		Handler(func(_ context.Context, in rulesAnalyzeInput) (*rulesAnalyzeResult, error) {
			return rulesAnalyze(in)
		})
	s.Tool("tokenops_rules_conflicts").
		Description("Detect redundancy, drift, and anti-pattern conflicts across rule artifacts. Returns Finding records without raw body text.").
		OutputSchema(rulesConflictsResult{}).
		Handler(func(_ context.Context, in rulesConflictsInput) (*rulesConflictsResult, error) {
			return rulesConflicts(in)
		})
	s.Tool("tokenops_rules_compress").
		Description("Distill the rule corpus by dropping redundant and near-duplicate sections. Returns per-document token totals before/after, quality score, and whether the result is accepted under the quality floor.").
		OutputSchema(rulesCompressResult{}).
		Handler(func(_ context.Context, in rulesCompressInput) (*rulesCompressResult, error) {
			return rulesCompress(in)
		})
	s.Tool("tokenops_rules_inject").
		Description("Preview the dynamic rule subset the router selects for a request context (workflow, agent, files, tools, keywords). Returns selections with rationale and budget metrics.").
		OutputSchema(ruleintel.Selection{}).
		Handler(func(_ context.Context, in rulesInjectInput) (*ruleintel.Selection, error) {
			return rulesInject(in)
		})
	return nil
}

func rulesAnalyze(in rulesAnalyzeInput) (*rulesAnalyzeResult, error) {
	prov, ok := ruleintel.ParseProvider(in.Provider)
	if !ok {
		return nil, inputError(fmt.Errorf("unknown provider %q", in.Provider))
	}
	corpus, err := callerCorpus(in.Root, in.RepoID)
	if err != nil {
		return nil, err
	}
	res, err := ruleintel.Analyze(corpus, prov)
	if err != nil {
		return nil, corpusError(err)
	}
	return &rulesAnalyzeResult{Documents: res.Documents, DuplicateGroups: res.DuplicateGroups}, nil
}

func rulesConflicts(in rulesConflictsInput) (*rulesConflictsResult, error) {
	corpus, err := callerCorpus(in.Root, in.RepoID)
	if err != nil {
		return nil, err
	}
	res, err := ruleintel.DetectConflicts(corpus)
	if err != nil {
		return nil, corpusError(err)
	}
	return &rulesConflictsResult{Findings: res.Findings}, nil
}

func rulesCompress(in rulesCompressInput) (*rulesCompressResult, error) {
	corpus, err := callerCorpus(in.Root, in.RepoID)
	if err != nil {
		return nil, err
	}
	res, err := ruleintel.Compress(corpus, ruleintel.CompressOptions{
		SimilarityThreshold: in.SimilarityThreshold,
		QualityFloor:        in.QualityFloor,
	})
	if err != nil {
		return nil, corpusError(err)
	}
	return &res, nil
}

func rulesInject(in rulesInjectInput) (*ruleintel.Selection, error) {
	corpus, err := callerCorpus(in.Root, in.RepoID)
	if err != nil {
		return nil, err
	}
	res, err := ruleintel.Inject(corpus, ruleintel.InjectQuery{
		TokenBudget:        in.TokenBudget,
		MinScore:           in.MinScore,
		IncludeGlobalScope: in.IncludeGlobal,
		LatencyBudget:      200 * time.Millisecond,
		WorkflowID:         in.WorkflowID,
		AgentID:            in.AgentID,
		FilePaths:          in.Files,
		Tools:              in.Tools,
		Keywords:           in.Keywords,
	})
	if err != nil {
		return nil, corpusError(err)
	}
	return res, nil
}

// callerCorpus names the rule corpus under a root the caller gave. A root
// that does not exist used to load as an empty corpus, so every rules tool
// answered "nothing here" for a mistyped path; it is refused instead.
func callerCorpus(root, repoID string) (ruleintel.Corpus, error) {
	if root != "" {
		if _, err := os.Stat(root); err != nil {
			return ruleintel.Corpus{}, inputError(fmt.Errorf("root %q: %w", root, err))
		}
	}
	return ruleintel.Corpus{Root: root, RepoID: repoID}, nil
}

// corpusError marks a corpus that could not be read as the caller's to
// fix; any other failure stays masked.
func corpusError(err error) error {
	var ce *ruleintel.CorpusError
	if errors.As(err, &ce) {
		return inputError(err)
	}
	return err
}
