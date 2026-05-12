// Package eval provides an optimization quality evaluation harness with
// offline fixture datasets and quality regression gates. It runs the
// optimizer pipeline against predefined test cases and reports per-
// optimizer quality scores, success rates, and drift metrics.
package eval

import (
	"encoding/json"
	"fmt"
)

// NewCase is the aggregate factory for a single eval Case. It enforces
// the invariants every downstream consumer depends on: non-empty ID,
// known provider, non-empty body, and at least one expectation set.
// External callers (suite loaders, tests) go through this factory so
// invariants live in one place.
func NewCase(id, description, provider, model string, body json.RawMessage, optimizers []string, expect Expect) (*Case, error) {
	if id == "" {
		return nil, fmt.Errorf("eval: Case requires ID")
	}
	if provider == "" {
		return nil, fmt.Errorf("eval: Case requires Provider")
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("eval: Case requires non-empty Body")
	}
	if expect.QualityScoreMin == 0 && expect.CompressionRatioMin == 0 && !expect.MustRecommend {
		return nil, fmt.Errorf("eval: Case requires at least one expectation (quality_score_min, compression_ratio_min, must_recommend)")
	}
	return &Case{
		ID:          id,
		Description: description,
		Provider:    provider,
		Model:       model,
		Body:        body,
		Optimizers:  optimizers,
		Expect:      expect,
	}, nil
}

// NewSuite is the aggregate factory for a Suite. Cases is non-nil but
// may be empty (deferred case registration is supported via AddCase).
// The factory rejects empty Name so downstream merge/report builders
// can rely on a stable identifier.
func NewSuite(name, description string, cases []Case) (*Suite, error) {
	if name == "" {
		return nil, fmt.Errorf("eval: Suite requires Name")
	}
	cp := make([]Case, len(cases))
	copy(cp, cases)
	return &Suite{Name: name, Description: description, Cases: cp}, nil
}

// AddCase appends c to the suite, returning an error when the case is
// invalid. The Suite stays consistent: every Case the harness sees has
// already passed NewCase's invariants.
func (s *Suite) AddCase(c *Case) error {
	if s == nil {
		return fmt.Errorf("eval: AddCase on nil Suite")
	}
	if c == nil {
		return fmt.Errorf("eval: AddCase requires non-nil Case")
	}
	s.Cases = append(s.Cases, *c)
	return nil
}

// CaseCount returns the number of cases the suite holds. Adapters that
// only need cardinality (dashboards, gate checks) should prefer this
// over accessing Cases directly.
func (s *Suite) CaseCount() int {
	if s == nil {
		return 0
	}
	return len(s.Cases)
}

// CasesCopy returns a defensive copy of the suite's cases so callers
// can iterate without risk of mutating the aggregate. Use this in any
// adapter that must inspect cases.
func (s *Suite) CasesCopy() []Case {
	if s == nil {
		return nil
	}
	out := make([]Case, len(s.Cases))
	copy(out, s.Cases)
	return out
}

type OptimizationType string

const (
	TypePromptCompress OptimizationType = "prompt_compress"
	TypeSemanticDedupe OptimizationType = "semantic_dedupe"
	TypeContextTrim    OptimizationType = "context_trim"
	TypeRetrievalPrune OptimizationType = "retrieval_prune"
	TypeModelRouter    OptimizationType = "model_router"
)

type Expect struct {
	QualityScoreMin     float64 `json:"quality_score_min"`
	CompressionRatioMin float64 `json:"compression_ratio_min"`
	MustRecommend       bool    `json:"must_recommend"`
}

type Case struct {
	ID          string          `json:"id"`
	Description string          `json:"description"`
	Provider    string          `json:"provider"`
	Model       string          `json:"model"`
	Body        json.RawMessage `json:"body"`
	Optimizers  []string        `json:"optimizers"`
	Expect      Expect          `json:"expect"`
}

type Suite struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Cases       []Case `json:"cases"`
}

type StepResult struct {
	CaseID      string  `json:"case_id"`
	Description string  `json:"description"`
	Optimizer   string  `json:"optimizer"`
	Passed      bool    `json:"passed"`
	Quality     float64 `json:"quality_score"`
	SavingsTok  int64   `json:"estimated_savings_tokens"`
	InputTok    int64   `json:"input_tokens"`
	Decision    string  `json:"decision"`
	Reason      string  `json:"reason"`
}

type OptimizerStat struct {
	TotalCases  int     `json:"total_cases"`
	PassedCases int     `json:"passed_cases"`
	AvgQuality  float64 `json:"avg_quality"`
	TotalSaved  int64   `json:"total_savings_tokens"`
	ApplyRate   float64 `json:"apply_rate"`
}

type Report struct {
	Name        string                             `json:"name"`
	TotalCases  int                                `json:"total_cases"`
	PassedCases int                                `json:"passed_cases"`
	SuccessRate float64                            `json:"success_rate"`
	Steps       []StepResult                       `json:"steps"`
	Optimizers  map[OptimizationType]OptimizerStat `json:"per_optimizer"`
}
