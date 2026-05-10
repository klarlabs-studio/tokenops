package eval

import (
	"context"
	"fmt"
	"strings"

	"github.com/felixgeelhaar/tokenops/internal/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/optimizer/contexttrim"
	"github.com/felixgeelhaar/tokenops/internal/optimizer/dedupe"
	"github.com/felixgeelhaar/tokenops/internal/optimizer/promptcompress"
	"github.com/felixgeelhaar/tokenops/internal/optimizer/retrievalprune"
	"github.com/felixgeelhaar/tokenops/internal/tokenizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

type PipelineBuilder struct {
	registry *tokenizer.Registry
	filters  []OptimizationType
}

func NewPipelineBuilder(opts ...OptimizationType) *PipelineBuilder {
	return &PipelineBuilder{
		registry: tokenizer.NewRegistry(),
		filters:  opts,
	}
}

func (b *PipelineBuilder) Build() *optimizer.Pipeline {
	all := []struct {
		typ OptimizationType
		opt optimizer.Optimizer
	}{
		{TypePromptCompress, promptcompress.New(promptcompress.Config{}, b.registry)},
		{TypeSemanticDedupe, dedupe.New(dedupe.Config{}, b.registry)},
		{TypeRetrievalPrune, retrievalprune.New(retrievalprune.Config{}, b.registry)},
		{TypeContextTrim, contexttrim.New(contexttrim.Config{}, b.registry)},
	}
	filterSet := make(map[OptimizationType]bool, len(b.filters))
	for _, f := range b.filters {
		filterSet[f] = true
	}
	opts := make([]optimizer.Optimizer, 0, len(all))
	for _, a := range all {
		if len(b.filters) > 0 && !filterSet[a.typ] {
			continue
		}
		opts = append(opts, a.opt)
	}
	return optimizer.NewPipeline(opts...)
}

type Runner struct {
	pipeline *optimizer.Pipeline
}

func NewRunner(pipeline *optimizer.Pipeline) *Runner {
	return &Runner{pipeline: pipeline}
}

func (r *Runner) RunSuite(ctx context.Context, suite *Suite) (*Report, error) {
	steps := make([]StepResult, 0, len(suite.Cases))
	passCount := 0

	for _, c := range suite.Cases {
		results := r.runCase(ctx, &c)
		for _, sr := range results {
			if sr.Passed {
				passCount++
			}
		}
		steps = append(steps, results...)
	}

	rate := 0.0
	if len(steps) > 0 {
		rate = float64(passCount) / float64(len(steps)) * 100
	}

	perOpt := r.optimizerStats(steps)

	return &Report{
		Name:        suite.Name,
		TotalCases:  len(steps),
		PassedCases: passCount,
		SuccessRate: rate,
		Steps:       steps,
		Optimizers:  perOpt,
	}, nil
}

func (r *Runner) runCase(ctx context.Context, c *Case) []StepResult {
	req := &optimizer.Request{
		Provider: eventschema.Provider(c.Provider),
		Model:    c.Model,
		Body:     c.Body,
		Mode:     optimizer.ModeInteractive,
	}
	out, err := r.pipeline.Run(ctx, req, nil)
	if err != nil {
		return []StepResult{{
			CaseID:   c.ID,
			Passed:   false,
			Decision: "error",
			Reason:   fmt.Sprintf("pipeline error: %v", err),
		}}
	}
	if len(out.Events) == 0 {
		return []StepResult{{
			CaseID:   c.ID,
			Passed:   !c.Expect.MustRecommend,
			Decision: "no_optimizations",
			Reason:   "no optimizer produced a recommendation",
		}}
	}

	inputTok := int64(len(c.Body)) / 4
	results := make([]StepResult, 0, len(out.Events))
	for _, ev := range out.Events {
		passed := r.checkExpectations(c, ev, inputTok)
		results = append(results, StepResult{
			CaseID:      c.ID,
			Description: c.Description,
			Optimizer:   string(ev.Kind),
			Passed:      passed,
			Quality:     ev.QualityScore,
			SavingsTok:  ev.EstimatedSavingsTokens,
			InputTok:    inputTok,
			Decision:    string(ev.Decision),
			Reason:      ev.Reason,
		})
	}
	return results
}

func (r *Runner) checkExpectations(c *Case, ev *eventschema.OptimizationEvent, inputTok int64) bool {
	ex := c.Expect
	if ex.QualityScoreMin > 0 && ev.QualityScore < ex.QualityScoreMin {
		return false
	}
	if ex.CompressionRatioMin > 0 && inputTok > 0 {
		ratio := float64(ev.EstimatedSavingsTokens) / float64(inputTok)
		if ratio < ex.CompressionRatioMin {
			return false
		}
	}
	if ex.MustRecommend && ev.Decision == eventschema.OptimizationDecisionSkipped &&
		ev.EstimatedSavingsTokens == 0 {
		return false
	}
	return true
}

func (r *Runner) optimizerStats(steps []StepResult) map[OptimizationType]OptimizerStat {
	byOpt := make(map[string][]StepResult)
	for _, s := range steps {
		key := s.Optimizer
		if key == "" {
			key = "unknown"
		}
		byOpt[key] = append(byOpt[key], s)
	}

	result := make(map[OptimizationType]OptimizerStat, len(byOpt))
	for key, sts := range byOpt {
		pass := 0
		var totalQ float64
		var totalS int64
		applied := 0
		for _, s := range sts {
			if s.Passed {
				pass++
			}
			totalQ += s.Quality
			totalS += s.SavingsTok
			if s.Decision == "applied" {
				applied++
			}
		}
		avgQ := totalQ / float64(len(sts))
		applyRate := float64(applied) / float64(len(sts)) * 100
		result[OptimizationType(key)] = OptimizerStat{
			TotalCases:  len(sts),
			PassedCases: pass,
			AvgQuality:  avgQ,
			TotalSaved:  totalS,
			ApplyRate:   applyRate,
		}
	}
	return result
}

func (r *Runner) PassRate(report *Report) float64 {
	if report.TotalCases == 0 {
		return 1.0
	}
	return float64(report.PassedCases) / float64(report.TotalCases)
}

func (r *Runner) QualityDrift(baseline, current *Report, optimizerType OptimizationType) float64 {
	bStat, bOk := baseline.Optimizers[optimizerType]
	cStat, cOk := current.Optimizers[optimizerType]
	if !bOk || !cOk {
		return 0
	}
	if bStat.AvgQuality == 0 {
		return 0
	}
	return (cStat.AvgQuality - bStat.AvgQuality) / bStat.AvgQuality * 100
}

func SuiteNames(suites []*Suite) string {
	names := make([]string, 0, len(suites))
	for _, s := range suites {
		names = append(names, s.Name)
	}
	return strings.Join(names, ", ")
}
