// Package scorecard defines the product wedge KPIs for TokenOps operators:
// first-value time, token efficiency uplift, and spend attribution
// completeness. The Scorecard aggregates these into a single assessment
// with grades per KPI and an overall health rating.
package scorecard

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

type Grade string

const (
	GradeA Grade = "A" // Excellent
	GradeB Grade = "B" // Good
	GradeC Grade = "C" // Fair
	GradeD Grade = "D" // Poor
	GradeF Grade = "F" // Failing
)

type KPIResult struct {
	Value     float64    `json:"value"`
	Unit      string     `json:"unit"`
	Grade     Grade      `json:"grade"`
	Threshold Thresholds `json:"thresholds"`
}

type Thresholds struct {
	Green  float64 `json:"green"`  // >= this = Grade A
	Yellow float64 `json:"yellow"` // >= this = Grade B/C; below = D/F
	Red    float64 `json:"red"`    // below this = Grade F
}

type Scorecard struct {
	GeneratedAt      time.Time `json:"generated_at"`
	FirstValueTime   KPIResult `json:"first_value_time"`
	TokenEfficiency  KPIResult `json:"token_efficiency_uplift"`
	SpendAttribution KPIResult `json:"spend_attribution_completeness"`
	OverallGrade     Grade     `json:"overall_grade"`
	BaselineRef      string    `json:"baseline_ref,omitempty"`
}

var DefaultThresholds = struct {
	FirstValueTime   Thresholds
	TokenEfficiency  Thresholds
	SpendAttribution Thresholds
}{
	FirstValueTime:   Thresholds{Green: 60, Yellow: 300, Red: 900},
	TokenEfficiency:  Thresholds{Green: 20, Yellow: 10, Red: 5},
	SpendAttribution: Thresholds{Green: 90, Yellow: 70, Red: 50},
}

func gradeValue(value float64, t Thresholds, higherIsBetter bool) Grade {
	if higherIsBetter {
		switch {
		case value >= t.Green:
			return GradeA
		case value >= t.Yellow:
			return GradeB
		case value >= t.Red:
			return GradeC
		default:
			return GradeF
		}
	}
	switch {
	case value <= t.Green:
		return GradeA
	case value <= t.Yellow:
		return GradeB
	case value <= t.Red:
		return GradeC
	default:
		return GradeF
	}
}

func overallGrade(results []Grade) Grade {
	if len(results) == 0 {
		return GradeF
	}
	order := []Grade{GradeA, GradeB, GradeC, GradeD, GradeF}
	rank := map[Grade]int{}
	for i, g := range order {
		rank[g] = i
	}
	worst := 0
	for _, g := range results {
		if r, ok := rank[g]; ok && r > worst {
			worst = r
		}
	}
	return order[worst]
}

func gradeFVT(seconds float64) Grade {
	return gradeValue(seconds, DefaultThresholds.FirstValueTime, false)
}

func gradeTEU(pct float64) Grade {
	return gradeValue(pct, DefaultThresholds.TokenEfficiency, true)
}

func gradeSAC(pct float64) Grade {
	return gradeValue(pct, DefaultThresholds.SpendAttribution, true)
}

func New(fvtSeconds, teuPct, sacPct float64, baselineRef string) *Scorecard {
	fvt := KPIResult{
		Value:     math.Round(fvtSeconds*10) / 10,
		Unit:      "seconds",
		Grade:     gradeFVT(fvtSeconds),
		Threshold: DefaultThresholds.FirstValueTime,
	}
	teu := KPIResult{
		Value:     math.Round(teuPct*10) / 10,
		Unit:      "%",
		Grade:     gradeTEU(teuPct),
		Threshold: DefaultThresholds.TokenEfficiency,
	}
	sac := KPIResult{
		Value:     math.Round(sacPct*10) / 10,
		Unit:      "%",
		Grade:     gradeSAC(sacPct),
		Threshold: DefaultThresholds.SpendAttribution,
	}
	return &Scorecard{
		GeneratedAt:      time.Now().UTC(),
		FirstValueTime:   fvt,
		TokenEfficiency:  teu,
		SpendAttribution: sac,
		OverallGrade:     overallGrade([]Grade{fvt.Grade, teu.Grade, sac.Grade}),
		BaselineRef:      baselineRef,
	}
}

func (s *Scorecard) MarshalJSON() ([]byte, error) {
	type alias Scorecard
	return json.MarshalIndent((*alias)(s), "", "  ")
}

func (s *Scorecard) String() string {
	return fmt.Sprintf(`Operator Wedge KPI Scorecard
Generated: %s

First-Value Time (seconds):          %.1f [%s]
Token Efficiency Uplift (%%):        %.1f [%s]
Spend Attribution Completeness (%%): %.1f [%s]

Overall Grade: %s
Baseline: %s

Thresholds (green / yellow / red):
  FVT:  ≤%.0f / ≤%.0f / ≤%.0f seconds
  TEU:  ≥%.0f%% / ≥%.0f%% / ≥%.0f%%
  SAC:  ≥%.0f%% / ≥%.0f%% / ≥%.0f%%
`,
		s.GeneratedAt.Format(time.RFC3339),
		s.FirstValueTime.Value, s.FirstValueTime.Grade,
		s.TokenEfficiency.Value, s.TokenEfficiency.Grade,
		s.SpendAttribution.Value, s.SpendAttribution.Grade,
		s.OverallGrade,
		baselineOrMissing(s.BaselineRef),
		s.FirstValueTime.Threshold.Green,
		s.FirstValueTime.Threshold.Yellow,
		s.FirstValueTime.Threshold.Red,
		s.TokenEfficiency.Threshold.Green,
		s.TokenEfficiency.Threshold.Yellow,
		s.TokenEfficiency.Threshold.Red,
		s.SpendAttribution.Threshold.Green,
		s.SpendAttribution.Threshold.Yellow,
		s.SpendAttribution.Threshold.Red,
	)
}

func baselineOrMissing(ref string) string {
	if ref == "" {
		return "(no baseline captured)"
	}
	return ref
}
