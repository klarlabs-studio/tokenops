package rules

import (
	"sort"
	"time"
)

// Profile is a named bundle of rule documents + router configuration that
// the benchmarking harness treats as one configuration under test. Profiles
// model "rule systems" the way scorecards are compared (e.g. "Go backend",
// "PR review", "refactor"). The harness is corpus-aware but does not need
// the replay engine — it scores each profile by the cost / coverage /
// quality of the rule subset it selects for each scenario.
type Profile struct {
	Name   string
	Docs   []*RuleDocument
	Router RouterConfig
}

// Scenario describes one workload the harness uses to grade profiles.
type Scenario struct {
	Name    string
	Signals SelectionSignals
	// Exposure provides the ROI engine with observed downstream metrics
	// for this scenario (request count, output tokens, retries, quality).
	// Profile-independent — the harness assumes the same downstream
	// behavior regardless of which rule subset was injected. Real
	// integration with replay data lives behind the
	// optimization-quality-evals-framework feature.
	Exposure Exposure
}

// ProfileScore aggregates a profile's result on a single scenario.
type ProfileScore struct {
	Profile        string
	Scenario       string
	Sections       int
	ContextTokens  int64
	TokensSaved    int64
	RetriesAvoided int64
	ROIScore       float64
	Truncated      bool
	BudgetHit      bool
}

// BenchmarkResult is the harness's full scoreboard.
type BenchmarkResult struct {
	Scores []ProfileScore
	// Winners maps scenario name to the profile with the highest ROIScore
	// for that scenario. Ties resolve by profile name (alphabetical).
	Winners map[string]string
}

// Benchmark executes every profile across every scenario.
type Benchmark struct {
	roi *ROIEngine
}

// NewBenchmark constructs a Benchmark with the given ROI config (zero
// value gives sensible defaults).
func NewBenchmark(cfg ROIConfig) *Benchmark {
	return &Benchmark{roi: NewROIEngine(cfg)}
}

// Run executes the harness. Profiles is consulted in order; scenarios
// equivalently. Empty inputs return an empty BenchmarkResult.
func (b *Benchmark) Run(profiles []Profile, scenarios []Scenario) *BenchmarkResult {
	res := &BenchmarkResult{Winners: map[string]string{}}
	bestByScenario := map[string]ProfileScore{}

	for _, p := range profiles {
		router := NewRouter(p.Router)
		for _, sc := range scenarios {
			sel := router.Select(p.Docs, sc.Signals)
			expo := sc.Exposure
			expo.RuleContextTokens = sel.TotalTokens
			if expo.Requests == 0 {
				expo.Requests = 1
			}
			if expo.WindowEnd.IsZero() {
				expo.WindowEnd = time.Now().UTC()
				expo.WindowStart = expo.WindowEnd.Add(-time.Hour)
			}
			ev := b.roi.Analyze([]Exposure{expo})[0]
			score := ProfileScore{
				Profile:        p.Name,
				Scenario:       sc.Name,
				Sections:       len(sel.Selections),
				ContextTokens:  ev.ContextTokens,
				TokensSaved:    ev.TokensSaved,
				RetriesAvoided: ev.RetriesAvoided,
				ROIScore:       ev.ROIScore,
				Truncated:      sel.Truncated,
				BudgetHit:      sel.BudgetHit,
			}
			res.Scores = append(res.Scores, score)
			cur, ok := bestByScenario[sc.Name]
			if !ok || score.ROIScore > cur.ROIScore ||
				(score.ROIScore == cur.ROIScore && score.Profile < cur.Profile) {
				bestByScenario[sc.Name] = score
			}
		}
	}

	for name, s := range bestByScenario {
		res.Winners[name] = s.Profile
	}

	sort.SliceStable(res.Scores, func(i, j int) bool {
		if res.Scores[i].Scenario != res.Scores[j].Scenario {
			return res.Scores[i].Scenario < res.Scores[j].Scenario
		}
		if res.Scores[i].ROIScore != res.Scores[j].ROIScore {
			return res.Scores[i].ROIScore > res.Scores[j].ROIScore
		}
		return res.Scores[i].Profile < res.Scores[j].Profile
	})

	return res
}
