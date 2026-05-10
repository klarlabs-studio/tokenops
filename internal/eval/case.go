// Package eval provides an optimization quality evaluation harness with
// offline fixture datasets and quality regression gates. It runs the
// optimizer pipeline against predefined test cases and reports per-
// optimizer quality scores, success rates, and drift metrics.
package eval

import "encoding/json"

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
