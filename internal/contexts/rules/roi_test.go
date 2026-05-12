package rules

import (
	"math"
	"testing"
	"time"
)

func TestROIEngineDefaultsApplied(t *testing.T) {
	e := NewROIEngine(ROIConfig{})
	if e.cfg.AssumedRetryRate <= 0 || e.cfg.AssumedSavingsPerRetry <= 0 || e.cfg.BaselineGrowthFactor <= 0 {
		t.Fatalf("defaults not applied: %+v", e.cfg)
	}
}

func TestROIEnginePositiveROIWhenRuleSavesTokens(t *testing.T) {
	now := time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC)
	exposure := Exposure{
		SourceID:             "repo:CLAUDE.md",
		SectionID:            "repo:CLAUDE.md#Testing",
		WindowStart:          now.Add(-time.Hour),
		WindowEnd:            now,
		Requests:             100,
		RuleContextTokens:    50, // 5000 token-context cost
		OutputTokens:         8000,
		BaselineOutputTokens: 10000, // rule saved 2000 tokens directly
		Retries:              5,     // assumed retry rate 0.10 -> 10 retries baseline
		QualityScore:         0.85,
		BaselineQualityScore: 0.80,
	}
	ev := NewROIEngine(ROIConfig{}).Analyze([]Exposure{exposure})[0]

	if ev.ContextTokens != 100*50 {
		t.Errorf("ContextTokens = %d, want %d", ev.ContextTokens, 100*50)
	}
	if ev.RetriesAvoided != 5 {
		t.Errorf("RetriesAvoided = %d, want 5", ev.RetriesAvoided)
	}
	wantSaved := int64(2000 + 5*800)
	if ev.TokensSaved != wantSaved {
		t.Errorf("TokensSaved = %d, want %d", ev.TokensSaved, wantSaved)
	}
	if ev.QualityDelta < 0.04 || ev.QualityDelta > 0.06 {
		t.Errorf("QualityDelta = %f, want ~0.05", ev.QualityDelta)
	}
	if ev.ROIScore <= 0 {
		t.Errorf("ROIScore = %f, want > 0", ev.ROIScore)
	}
	// 2000/10000 = 0.20 context reduction.
	if math.Abs(ev.ContextReduction-0.20) > 1e-9 {
		t.Errorf("ContextReduction = %f, want 0.20", ev.ContextReduction)
	}
}

func TestROIEngineClampsNegativeSavings(t *testing.T) {
	exposure := Exposure{
		SourceID:             "repo:X.md",
		Requests:             10,
		RuleContextTokens:    100,
		OutputTokens:         1000,
		BaselineOutputTokens: 500, // observed > baseline: rule made it worse
	}
	ev := NewROIEngine(ROIConfig{}).Analyze([]Exposure{exposure})[0]
	if ev.TokensSaved < 0 {
		t.Errorf("TokensSaved = %d, want >= 0 after clamp", ev.TokensSaved)
	}
}

func TestROIEngineUsesBaselineGrowthFactorWhenNoBaseline(t *testing.T) {
	exposure := Exposure{
		SourceID:          "repo:X.md",
		Requests:          10,
		RuleContextTokens: 10,
		OutputTokens:      1000,
		Retries:           1, // matches assumedRetries (10 * 0.10) so retry term is 0
		// No BaselineOutputTokens: engine assumes 1000 * 1.08 = 1080.
	}
	ev := NewROIEngine(ROIConfig{}).Analyze([]Exposure{exposure})[0]
	if ev.TokensSaved < 70 || ev.TokensSaved > 90 {
		t.Errorf("TokensSaved = %d, want ~80 (1080-1000)", ev.TokensSaved)
	}
	if ev.RetriesAvoided != 0 {
		t.Errorf("RetriesAvoided = %d, want 0", ev.RetriesAvoided)
	}
}

func TestROIEngineZeroContextSafe(t *testing.T) {
	ev := NewROIEngine(ROIConfig{}).Analyze([]Exposure{{SourceID: "x"}})[0]
	if ev.ROIScore != 0 {
		t.Errorf("ROIScore for zero-context exposure = %f, want 0", ev.ROIScore)
	}
}
