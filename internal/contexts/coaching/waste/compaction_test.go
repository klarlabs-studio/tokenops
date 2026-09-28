package waste

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// traceOf builds a claude-code trace from per-step context sizes, each
// step costing cost dollars.
func traceOf(ctx []int64, cost float64) *workflow.Trace {
	t := &workflow.Trace{WorkflowID: "claude-code:p:s"}
	for i, c := range ctx {
		t.Steps = append(t.Steps, workflow.Step{Index: i, Prompt: &eventschema.PromptEvent{InputTokens: c}, ListCostUSD: cost})
		t.MaxContextSize = max(t.MaxContextSize, c)
	}
	t.StepCount = len(t.Steps)
	return t
}

func repeat(n int, v int64) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func compactEarlier(evs []*eventschema.CoachingEvent) *eventschema.CoachingEvent {
	for _, e := range evs {
		if e.Kind == eventschema.CoachingKindCompactEarlier {
			return e
		}
	}
	return nil
}

func TestCompactEarlierFlagsALongStretchAboveTheLine(t *testing.T) {
	ev := compactEarlier(New(Config{}).Detect(traceOf(repeat(30, 800_000), 0.40)))
	if ev == nil {
		t.Fatal("30 steps at 800k did not report compact_earlier")
	}
	// No compaction in the trace: baseline is 600k/8 = 75k, and compacting
	// at 600k would average (75k+600k)/2 = 337.5k, so each 800k step
	// carried 462.5k extra, 462.5/800 of its cost.
	if ev.EstimatedSavingsTokens != 30*462_500 {
		t.Errorf("tokens = %d; want %d", ev.EstimatedSavingsTokens, 30*462_500)
	}
	if want := 30 * 0.40 * 462.5 / 800.0; ev.EstimatedSavingsUSD < want-1e-9 || ev.EstimatedSavingsUSD > want+1e-9 {
		t.Errorf("usd = %f; want %f", ev.EstimatedSavingsUSD, want)
	}
	if ev.ReplayMetadata["longest_stretch"] != "30" {
		t.Errorf("metadata = %v", ev.ReplayMetadata)
	}
}

func TestCompactEarlierIgnoresShortStretches(t *testing.T) {
	ctx := append(repeat(15, 800_000), 70_000)
	ctx = append(ctx, repeat(15, 800_000)...)
	if ev := compactEarlier(New(Config{}).Detect(traceOf(ctx, 0.4))); ev != nil {
		t.Fatalf("two 15-step stretches split by a compaction reported: %s", ev.Details)
	}
}

func TestCompactEarlierUsesTheSessionsOwnBaseline(t *testing.T) {
	ctx := append(repeat(5, 700_000), 200_000)
	ctx = append(ctx, repeat(25, 700_000)...)
	ev := compactEarlier(New(Config{}).Detect(traceOf(ctx, 0)))
	if ev == nil {
		t.Fatal("not reported")
	}
	if ev.ReplayMetadata["baseline_tokens"] != "200000" {
		t.Errorf("baseline = %s; want the session's 200k post-compaction size", ev.ReplayMetadata["baseline_tokens"])
	}
	if ev.EstimatedSavingsUSD != 0 {
		t.Errorf("unpriced steps produced $%f", ev.EstimatedSavingsUSD)
	}
}

func TestCompactEarlierNeedsAProfile(t *testing.T) {
	tr := traceOf(repeat(30, 800_000), 0.4)
	tr.WorkflowID = "agent-x:1"
	if ev := compactEarlier(New(Config{}).Detect(tr)); ev != nil {
		t.Fatal("reported for a workflow without a compaction threshold")
	}
}

// An operator profile written before compact_at_tokens existed keeps the
// built-in threshold; one that sets it wins.
func TestOperatorProfilesAndCompactAt(t *testing.T) {
	tr := traceOf(repeat(30, 500_000), 0.4)
	old := Config{Profiles: []Profile{{WorkflowPrefix: "claude-code:", MaxContextTokens: 950_000}}}
	if ev := compactEarlier(New(old).Detect(tr)); ev != nil {
		t.Fatal("500k reported against the built-in 600k line")
	}
	tuned := Config{Profiles: []Profile{{WorkflowPrefix: "claude-code:", CompactAtTokens: 400_000}}}
	if ev := compactEarlier(New(tuned).Detect(tr)); ev == nil {
		t.Fatal("operator's 400k line ignored")
	}
}
