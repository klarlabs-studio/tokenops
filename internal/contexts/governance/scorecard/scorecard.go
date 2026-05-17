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
	// Agent-workflow KPIs (added v0.19). Grade is "" when the live
	// computation didn't supply the metric; renderers skip empty
	// KPIs so the operator doesn't see a phantom F.
	CacheHitRatio        KPIResult `json:"cache_hit_ratio,omitempty"`
	ConfirmationGateRate KPIResult `json:"confirmation_gate_rate,omitempty"`
	RegenerateRate       KPIResult `json:"regenerate_rate,omitempty"`
	OverallGrade         Grade     `json:"overall_grade"`
	BaselineRef          string    `json:"baseline_ref,omitempty"`
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
	FirstValueTime       Thresholds
	TokenEfficiency      Thresholds
	SpendAttribution     Thresholds
	CacheHitRatio        Thresholds
	ConfirmationGateRate Thresholds
	RegenerateRate       Thresholds
}{
	FirstValueTime:   Thresholds{Green: 60, Yellow: 300, Red: 900},
	TokenEfficiency:  Thresholds{Green: 20, Yellow: 10, Red: 5},
	SpendAttribution: Thresholds{Green: 90, Yellow: 70, Red: 50},
	// CHR: % of input tokens that came from cache reads. Higher is
	// better. For agent workloads >90% indicates strong context reuse.
	CacheHitRatio: Thresholds{Green: 90, Yellow: 70, Red: 50},
	// CGR: % of user prompts that are pure acks (yes/no/continue).
	// Lower is better. >30% means the agent is asking too many
	// confirmation gates the operator could pre-empt.
	ConfirmationGateRate: Thresholds{Green: 10, Yellow: 20, Red: 30},
	// RGR: % of user prompts that reject the prior agent output
	// (try again, redo, do it differently). Lower is better. >10%
	// means the agent is shipping work the operator rejects.
	RegenerateRate: Thresholds{Green: 5, Yellow: 10, Red: 20},
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

func gradeCHR(pct float64) Grade {
	return gradeValue(pct, DefaultThresholds.CacheHitRatio, true)
}

func gradeCGR(pct float64) Grade {
	return gradeValue(pct, DefaultThresholds.ConfirmationGateRate, false)
}

func gradeRGR(pct float64) Grade {
	return gradeValue(pct, DefaultThresholds.RegenerateRate, false)
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

// AgentKPIInputs carries the v0.19 agent-workflow KPIs. Each field
// is optional; pass NaN (or use the zero value with the *Computed
// flag) to skip a metric. New(...) only populates Scorecard fields
// for metrics with a real value.
type AgentKPIInputs struct {
	CacheHitRatioPct         float64
	CacheHitRatioComputed    bool
	ConfirmationGateRatePct  float64
	ConfirmationGateComputed bool
	RegenerateRatePct        float64
	RegenerateComputed       bool
}

func New(fvtSeconds, teuPct, sacPct float64, baselineRef string) *Scorecard {
	return NewWithAgentKPIs(fvtSeconds, teuPct, sacPct, AgentKPIInputs{}, baselineRef)
}

// NewWithAgentKPIs is the v0.19 entry point that accepts the
// agent-workflow metrics alongside the original FVT/TEU/SAC trio.
// Callers that don't have the new metrics use New(...), which is a
// thin wrapper around this with a zero AgentKPIInputs.
func NewWithAgentKPIs(fvtSeconds, teuPct, sacPct float64, agent AgentKPIInputs, baselineRef string) *Scorecard {
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
	grades := []Grade{fvt.Grade, teu.Grade, sac.Grade}
	sc := &Scorecard{
		GeneratedAt:      time.Now().UTC(),
		FirstValueTime:   fvt,
		TokenEfficiency:  teu,
		SpendAttribution: sac,
		BaselineRef:      baselineRef,
	}
	if agent.CacheHitRatioComputed {
		k := KPIResult{
			Name:        "Cache Hit Ratio (CHR)",
			Description: "Percent of input tokens served from cache reads. Higher is better.",
			Value:       math.Round(agent.CacheHitRatioPct*10) / 10,
			Unit:        "%",
			Grade:       gradeCHR(agent.CacheHitRatioPct),
			Threshold:   DefaultThresholds.CacheHitRatio,
		}
		sc.CacheHitRatio = k
		grades = append(grades, k.Grade)
	}
	if agent.ConfirmationGateComputed {
		k := KPIResult{
			Name:        "Confirmation Gate Rate (CGR)",
			Description: "Percent of user prompts that are pure acks (yes/no/continue). Lower is better.",
			Value:       math.Round(agent.ConfirmationGateRatePct*10) / 10,
			Unit:        "%",
			Grade:       gradeCGR(agent.ConfirmationGateRatePct),
			Threshold:   DefaultThresholds.ConfirmationGateRate,
		}
		sc.ConfirmationGateRate = k
		grades = append(grades, k.Grade)
	}
	if agent.RegenerateComputed {
		k := KPIResult{
			Name:        "Regenerate Rate (RGR)",
			Description: "Percent of user prompts that reject the prior agent output. Lower is better.",
			Value:       math.Round(agent.RegenerateRatePct*10) / 10,
			Unit:        "%",
			Grade:       gradeRGR(agent.RegenerateRatePct),
			Threshold:   DefaultThresholds.RegenerateRate,
		}
		sc.RegenerateRate = k
		grades = append(grades, k.Grade)
	}
	sc.OverallGrade = overallGrade(grades)
	return sc
}

// MarshalJSON renders Scorecard with empty KPI blocks dropped when
// the overall grade is `warming_up` — consumers see the checklist
// and nothing else. KPIResult is a value type so omitempty alone
// can't suppress it; this MarshalJSON hand-builds the wire shape
// to keep the empty-state JSON tidy.
func (s *Scorecard) MarshalJSON() ([]byte, error) {
	if s.OverallGrade == GradeWarmingUp {
		warm := struct {
			GeneratedAt  time.Time       `json:"generated_at"`
			OverallGrade Grade           `json:"overall_grade"`
			BaselineRef  string          `json:"baseline_ref,omitempty"`
			Checklist    []ChecklistItem `json:"checklist"`
		}{
			GeneratedAt:  s.GeneratedAt,
			OverallGrade: s.OverallGrade,
			BaselineRef:  s.BaselineRef,
			Checklist:    s.Checklist,
		}
		return json.MarshalIndent(warm, "", "  ")
	}
	// Optional agent KPIs: only marshal blocks whose Grade is set
	// (i.e. the computation actually ran). Hand-build the wire shape
	// rather than introduce *KPIResult pointers across the API.
	out := struct {
		GeneratedAt          time.Time  `json:"generated_at"`
		FirstValueTime       KPIResult  `json:"first_value_time"`
		TokenEfficiency      KPIResult  `json:"token_efficiency_uplift"`
		SpendAttribution     KPIResult  `json:"spend_attribution_completeness"`
		CacheHitRatio        *KPIResult `json:"cache_hit_ratio,omitempty"`
		ConfirmationGateRate *KPIResult `json:"confirmation_gate_rate,omitempty"`
		RegenerateRate       *KPIResult `json:"regenerate_rate,omitempty"`
		OverallGrade         Grade      `json:"overall_grade"`
		BaselineRef          string     `json:"baseline_ref,omitempty"`
	}{
		GeneratedAt:      s.GeneratedAt,
		FirstValueTime:   s.FirstValueTime,
		TokenEfficiency:  s.TokenEfficiency,
		SpendAttribution: s.SpendAttribution,
		OverallGrade:     s.OverallGrade,
		BaselineRef:      s.BaselineRef,
	}
	if s.CacheHitRatio.Grade != "" {
		k := s.CacheHitRatio
		out.CacheHitRatio = &k
	}
	if s.ConfirmationGateRate.Grade != "" {
		k := s.ConfirmationGateRate
		out.ConfirmationGateRate = &k
	}
	if s.RegenerateRate.Grade != "" {
		k := s.RegenerateRate
		out.RegenerateRate = &k
	}
	return json.MarshalIndent(out, "", "  ")
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
	var b strings.Builder
	fmt.Fprintf(&b, "Operator Wedge KPI Scorecard\nGenerated: %s\n\n",
		s.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "FVT — First-Value Time (seconds):           %.1f [%s]\n",
		s.FirstValueTime.Value, s.FirstValueTime.Grade)
	fmt.Fprintf(&b, "TEU — Token Efficiency Uplift (%%):          %.1f [%s]\n",
		s.TokenEfficiency.Value, s.TokenEfficiency.Grade)
	fmt.Fprintf(&b, "SAC — Spend Attribution Completeness (%%):   %.1f [%s]\n",
		s.SpendAttribution.Value, s.SpendAttribution.Grade)
	if s.CacheHitRatio.Grade != "" {
		fmt.Fprintf(&b, "CHR — Cache Hit Ratio (%%):                  %.1f [%s]\n",
			s.CacheHitRatio.Value, s.CacheHitRatio.Grade)
	}
	if s.ConfirmationGateRate.Grade != "" {
		fmt.Fprintf(&b, "CGR — Confirmation Gate Rate (%%):           %.1f [%s]\n",
			s.ConfirmationGateRate.Value, s.ConfirmationGateRate.Grade)
	}
	if s.RegenerateRate.Grade != "" {
		fmt.Fprintf(&b, "RGR — Regenerate Rate (%%):                  %.1f [%s]\n",
			s.RegenerateRate.Value, s.RegenerateRate.Grade)
	}
	fmt.Fprintf(&b, "\nOverall Grade: %s\nBaseline: %s\n", s.OverallGrade, baselineOrMissing(s.BaselineRef))
	fmt.Fprintf(&b, "\nThresholds (green / yellow / red):\n")
	fmt.Fprintf(&b, "  FVT:  ≤%.0f / ≤%.0f / ≤%.0f seconds\n",
		s.FirstValueTime.Threshold.Green, s.FirstValueTime.Threshold.Yellow, s.FirstValueTime.Threshold.Red)
	fmt.Fprintf(&b, "  TEU:  ≥%.0f%% / ≥%.0f%% / ≥%.0f%%\n",
		s.TokenEfficiency.Threshold.Green, s.TokenEfficiency.Threshold.Yellow, s.TokenEfficiency.Threshold.Red)
	fmt.Fprintf(&b, "  SAC:  ≥%.0f%% / ≥%.0f%% / ≥%.0f%%\n",
		s.SpendAttribution.Threshold.Green, s.SpendAttribution.Threshold.Yellow, s.SpendAttribution.Threshold.Red)
	if s.CacheHitRatio.Grade != "" {
		fmt.Fprintf(&b, "  CHR:  ≥%.0f%% / ≥%.0f%% / ≥%.0f%%\n",
			s.CacheHitRatio.Threshold.Green, s.CacheHitRatio.Threshold.Yellow, s.CacheHitRatio.Threshold.Red)
	}
	if s.ConfirmationGateRate.Grade != "" {
		fmt.Fprintf(&b, "  CGR:  ≤%.0f%% / ≤%.0f%% / ≤%.0f%%\n",
			s.ConfirmationGateRate.Threshold.Green, s.ConfirmationGateRate.Threshold.Yellow, s.ConfirmationGateRate.Threshold.Red)
	}
	if s.RegenerateRate.Grade != "" {
		fmt.Fprintf(&b, "  RGR:  ≤%.0f%% / ≤%.0f%% / ≤%.0f%%\n",
			s.RegenerateRate.Threshold.Green, s.RegenerateRate.Threshold.Yellow, s.RegenerateRate.Threshold.Red)
	}
	return b.String()
}

func baselineOrMissing(ref string) string {
	if ref == "" {
		return "(no baseline captured)"
	}
	return ref
}
