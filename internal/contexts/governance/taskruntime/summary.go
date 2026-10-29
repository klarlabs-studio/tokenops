package taskruntime

import (
	"math"
	"sort"
)

// PercentileMethod names how Distribution's percentiles are taken.
const PercentileMethod = "nearest-rank"

// Distribution summarises one figure over a group's samples.
type Distribution struct {
	// N is the number of samples: the denominator.
	N      int      `json:"n"`
	Median *float64 `json:"median"`
	P90    *float64 `json:"p90"`
	Method string   `json:"method"`
}

// distribution takes the nearest-rank median and p90; both are null for
// no samples, never zero.
func distribution(vs []float64) Distribution {
	d := Distribution{N: len(vs), Method: PercentileMethod}
	if len(vs) == 0 {
		return d
	}
	s := append([]float64(nil), vs...)
	sort.Float64s(s)
	d.Median, d.P90 = nearestRank(s, 0.5), nearestRank(s, 0.9)
	return d
}

// nearestRank is the smallest sample with at least p of the samples at
// or below it.
func nearestRank(sorted []float64, p float64) *float64 {
	rank := int(math.Ceil(p * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	v := sorted[rank-1]
	return &v
}

// StatusCounts counts a group's tasks by status.
type StatusCounts struct {
	Completed    int `json:"completed"`
	LastObserved int `json:"last_observed"`
	Open         int `json:"open"`
}

// CategorySummary is one category across a group's runtime samples.
type CategorySummary struct {
	Category Category `json:"category"`
	// Known is how many samples record the category; Unknown how many do
	// not. Known + Unknown is the group's runtime samples.
	Known   int `json:"known"`
	Unknown int `json:"unknown"`
	// Seconds and Share are over the Known samples only.
	Seconds Distribution `json:"seconds"`
	Share   Distribution `json:"share_of_observed"`
}

// Group summarises a cohort of tasks: one harness, model and role, or
// every task when those are empty.
type Group struct {
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
	Role    string `json:"role,omitempty"`

	Tasks  int          `json:"tasks"`
	Status StatusCounts `json:"status"`
	// RuntimeSamples are the tasks a duration is summarised over:
	// answered, and not open. ExcludedOpen and ExcludedNoTurns are the
	// rest, so the denominators add up to Tasks.
	RuntimeSamples  int `json:"runtime_samples"`
	ExcludedOpen    int `json:"excluded_open"`
	ExcludedNoTurns int `json:"excluded_no_turns"`

	// Observed is first instruction to last observed activity, over the
	// runtime samples; Completed over the completed ones alone.
	Observed  Distribution `json:"observed_seconds"`
	Completed Distribution `json:"completed_seconds"`
	// HarnessTTFT is the time to first token the harness recorded, over
	// the samples that carry one.
	HarnessTTFT Distribution      `json:"harness_ttft_seconds"`
	Categories  []CategorySummary `json:"categories"`
	// ToolCriticalPath and ToolResource are over the samples with a
	// timed tool call.
	ToolCriticalPath Distribution `json:"tool_critical_path_seconds"`
	ToolResource     Distribution `json:"tool_resource_seconds"`

	// HeuristicClean counts runtime samples with no friction signal. A
	// heuristic, never verified success.
	HeuristicClean int    `json:"heuristic_clean"`
	HeuristicLabel string `json:"heuristic_label"`
	// VerifiedSuccess counts tasks something independently verified.
	// Transcripts record no verification, so it is always null.
	VerifiedSuccess *int `json:"verified_success"`

	// UsageSamples are the runtime samples with recorded usage: the
	// denominator of the cost and token figures. UsageUnpriced counts the
	// ones with a turn no list price covers, which understate.
	UsageSamples  int `json:"usage_samples"`
	UsageUnpriced int `json:"usage_unpriced"`
	// APIEquivalentUSD is each task's usage at list prices; BilledUSD what
	// it was billed; Tokens its input and output tokens.
	APIEquivalentUSD Distribution `json:"api_equivalent_usd"`
	BilledUSD        Distribution `json:"billed_usd"`
	Tokens           Distribution `json:"tokens"`
}

// Summarise rolls tasks up into one group.
func Summarise(tasks []Task) Group {
	g := Group{Tasks: len(tasks),
		HeuristicLabel: "heuristic: no friction signal in the transcript; not verified success"}
	observed := make([]float64, 0, len(tasks))
	var completed, ttft, critical, resource, value, billed, tokens []float64
	perCat := map[Category]*struct {
		known, unknown int
		secs, shares   []float64
	}{}
	for _, c := range Categories {
		perCat[c] = &struct {
			known, unknown int
			secs, shares   []float64
		}{}
	}
	for _, t := range tasks {
		switch t.Status {
		case StatusCompleted:
			g.Status.Completed++
		case StatusOpen:
			g.Status.Open++
		default:
			g.Status.LastObserved++
		}
		switch {
		case t.ObservedSeconds == nil:
			g.ExcludedNoTurns++
			continue
		case t.Status == StatusOpen:
			g.ExcludedOpen++
			continue
		}
		g.RuntimeSamples++
		observed = append(observed, *t.ObservedSeconds)
		if t.Status == StatusCompleted {
			completed = append(completed, *t.ObservedSeconds)
		}
		if t.HarnessTTFTSeconds != nil {
			ttft = append(ttft, *t.HarnessTTFTSeconds)
		}
		if t.Tools.CriticalPathSeconds != nil {
			critical = append(critical, *t.Tools.CriticalPathSeconds)
			resource = append(resource, *t.Tools.ResourceSeconds)
		}
		if t.Heuristic.Clean {
			g.HeuristicClean++
		}
		if u := t.Usage; u != nil {
			g.UsageSamples++
			if u.UnpricedTurns > 0 {
				g.UsageUnpriced++
			}
			value = append(value, u.APIEquivalentUSD)
			billed = append(billed, u.BilledUSD)
			tokens = append(tokens, float64(u.InputTokens+u.OutputTokens))
		}
		for _, ct := range t.Categories {
			pc := perCat[ct.Category]
			if pc == nil {
				continue
			}
			if ct.Seconds == nil {
				pc.unknown++
				continue
			}
			pc.known++
			pc.secs = append(pc.secs, *ct.Seconds)
			pc.shares = append(pc.shares, *ct.Share)
		}
	}
	g.Observed, g.Completed, g.HarnessTTFT = distribution(observed), distribution(completed), distribution(ttft)
	g.ToolCriticalPath, g.ToolResource = distribution(critical), distribution(resource)
	g.APIEquivalentUSD, g.BilledUSD, g.Tokens = distribution(value), distribution(billed), distribution(tokens)
	for _, c := range Categories {
		pc := perCat[c]
		g.Categories = append(g.Categories, CategorySummary{
			Category: c, Known: pc.known, Unknown: pc.unknown,
			Seconds: distribution(pc.secs), Share: distribution(pc.shares),
		})
	}
	return g
}

// GroupBy summarises tasks per harness, model and role, largest group
// first.
func GroupBy(tasks []Task) []Group {
	type k struct{ harness, model, role string }
	by := map[k][]Task{}
	var order []k
	for _, t := range tasks {
		key := k{t.Harness, t.Model, t.Role}
		if _, ok := by[key]; !ok {
			order = append(order, key)
		}
		by[key] = append(by[key], t)
	}
	out := make([]Group, 0, len(order))
	for _, key := range order {
		g := Summarise(by[key])
		g.Harness, g.Model, g.Role = key.harness, key.model, key.role
		out = append(out, g)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Tasks > out[j].Tasks })
	return out
}
