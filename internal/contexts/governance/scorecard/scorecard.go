// Package scorecard defines the product wedge KPIs for TokenOps operators:
// first-value time, token efficiency uplift, and spend attribution
// completeness. The Scorecard aggregates these into a single assessment
// with grades per KPI and an overall health rating.
package scorecard

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
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
	// Name is the full, human-readable label (e.g. "First Value Time").
	// Surfaced so dashboards and CLI output can render the expansion
	// next to the abbreviation; callers that already know the shape can
	// ignore it.
	Name string `json:"name"`
	// Description is a one-line definition of what the KPI measures
	// and which direction is healthy. Kept short on purpose so it can
	// sit inline in a table row.
	Description string     `json:"description"`
	Value       float64    `json:"value"`
	Unit        string     `json:"unit"`
	Grade       Grade      `json:"grade"`
	Threshold   Thresholds `json:"thresholds"`
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
	// Checklist is populated only when no KPI was computed from real
	// data. Renderers should show the checklist instead of an F grade
	// — defaulted KPIs are not a verdict on the operator.
	Checklist []ChecklistItem `json:"checklist,omitempty"`
}

// ChecklistItem is one step in the first-week activation path the
// scorecard renders when no real telemetry exists yet. Order is
// significant: render in slice order.
type ChecklistItem struct {
	Description string `json:"description"`
	NextAction  string `json:"next_action"`
	Completed   bool   `json:"completed"`
}

// GradeWarmingUp is the synthetic grade used when no KPI has real
// data backing it. Renderers special-case this to skip the F.
const GradeWarmingUp Grade = "warming_up"

// FirstWeekChecklist is the canonical activation ladder shown when
// LiveKPIs has nothing computed. Operators reading the scorecard with
// an empty store see "what to do next", not a verdict.
var FirstWeekChecklist = []ChecklistItem{
	{
		Description: "Configure at least one provider URL so requests flow through the local proxy",
		NextAction:  "tokenops provider set anthropic https://api.anthropic.com",
	},
	{
		Description: "Bind your subscription plan so headroom math sees real activity",
		NextAction:  "tokenops plan set anthropic claude-max-20x",
	},
	{
		Description: "Accumulate 7 days of attributed events for spend forecasting",
		NextAction:  "use the agent normally; metrics warm up automatically",
	},
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

// NewWarmingUp returns a Scorecard variant for the empty-data case:
// every KPI is omitted, OverallGrade is GradeWarmingUp, and the
// Checklist points the operator at the next-action commands. Used by
// Build/BuildFromStore when no KPI was computed from real telemetry
// — defaulted KPIs are not a verdict.
func NewWarmingUp(baselineRef string) *Scorecard {
	cl := make([]ChecklistItem, len(FirstWeekChecklist))
	copy(cl, FirstWeekChecklist)
	return &Scorecard{
		GeneratedAt:  time.Now().UTC(),
		OverallGrade: GradeWarmingUp,
		BaselineRef:  baselineRef,
		Checklist:    cl,
	}
}

func New(fvtSeconds, teuPct, sacPct float64, baselineRef string) *Scorecard {
	fvt := KPIResult{
		Name:        "First Value Time (FVT)",
		Description: "Seconds from install to first measurable insight. Lower is better.",
		Value:       math.Round(fvtSeconds*10) / 10,
		Unit:        "seconds",
		Grade:       gradeFVT(fvtSeconds),
		Threshold:   DefaultThresholds.FirstValueTime,
	}
	teu := KPIResult{
		Name:        "Token Efficiency Uplift (TEU)",
		Description: "Percent of input tokens saved by the optimizer (saved / input). Higher is better.",
		Value:       math.Round(teuPct*10) / 10,
		Unit:        "%",
		Grade:       gradeTEU(teuPct),
		Threshold:   DefaultThresholds.TokenEfficiency,
	}
	sac := KPIResult{
		Name:        "Spend Attribution Completeness (SAC)",
		Description: "Percent of spend with workflow or agent attribution. Higher is better.",
		Value:       math.Round(sacPct*10) / 10,
		Unit:        "%",
		Grade:       gradeSAC(sacPct),
		Threshold:   DefaultThresholds.SpendAttribution,
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
	if s.OverallGrade == GradeWarmingUp {
		var b strings.Builder
		fmt.Fprintf(&b, "Operator Wedge KPI Scorecard\nGenerated: %s\n\nScorecard: warming up (no real events observed yet)\n\nFirst-week checklist:\n",
			s.GeneratedAt.Format(time.RFC3339))
		for i, item := range s.Checklist {
			marker := "[ ]"
			if item.Completed {
				marker = "[x]"
			}
			fmt.Fprintf(&b, "  %s %d. %s\n     run: %s\n", marker, i+1, item.Description, item.NextAction)
		}
		fmt.Fprintf(&b, "\nBaseline: %s\n", baselineOrMissing(s.BaselineRef))
		return b.String()
	}
	return fmt.Sprintf(`Operator Wedge KPI Scorecard
Generated: %s

FVT — First-Value Time (seconds):           %.1f [%s]
TEU — Token Efficiency Uplift (%%):          %.1f [%s]
SAC — Spend Attribution Completeness (%%):   %.1f [%s]

Overall Grade: %s
Baseline: %s

Definitions:
  FVT — %s
  TEU — %s
  SAC — %s

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
		s.FirstValueTime.Description,
		s.TokenEfficiency.Description,
		s.SpendAttribution.Description,
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
