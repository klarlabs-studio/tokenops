package workestimate

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/work/windowshare"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

var t0 = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "w.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func anthropicConfig() *config.Config {
	return &config.Config{Plans: map[string]string{"anthropic": "claude-max"}}
}

// meter records a Claude usage-meter reading of the five-hour window.
func meter(t *testing.T, st *sqlite.Store, at time.Time, usedPct float64, resetsAt time.Time) {
	t.Helper()
	env := &eventschema.Envelope{
		ID: fmt.Sprintf("meter-%d", at.UnixNano()), SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypePrompt, Timestamp: at, Source: "claude-usage-meter",
		Attributes: map[string]string{
			"five_hour_used_pct": strconv.FormatFloat(usedPct, 'f', -1, 64),
			"five_hour_reset_at": resetsAt.Format(time.RFC3339),
		},
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, Status: 200},
	}
	if err := st.Append(context.Background(), env); err != nil {
		t.Fatal(err)
	}
}

// seedActuals records n measured tasks of class edit in scope tokenops.
func seedActuals(t *testing.T, st *sqlite.Store, shares ...float64) {
	t.Helper()
	for i, s := range shares {
		rec := actualRecord{
			WorkflowID: fmt.Sprintf("workflow:past-%d", i), TaskClass: "edit", Scope: "tokenops",
			Provider: "anthropic", WindowSeconds: int64((5 * time.Hour).Seconds()),
			Actual: windowshare.Actual{Measured: true, SharePct: s},
		}
		env, err := eventschema.NewDomainEnvelope(KindActual, rec, t0.Add(-time.Duration(i+1)*time.Hour), source, eventschema.Association{})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Append(context.Background(), env); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPrepareSuppressesTheEstimateWithoutHistory(t *testing.T) {
	st := openStore(t)
	meter(t, st, t0.Add(-time.Minute), 62, t0.Add(70*time.Minute))
	got, err := Prepare(context.Background(), Deps{Config: anthropicConfig(), Store: st}, PrepareRequest{
		WorkflowID: "workflow:new", Instruction: "add a flag to the status command", Scope: "tokenops",
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != string(windowshare.StatusInsufficient) || got.Estimate.Estimated() {
		t.Fatalf("preparation = %+v, want the estimate suppressed with no history", got)
	}
	if got.TaskClass != "edit" || !got.WindowKnown || got.LeftPct != 38 || got.ResetsIn != "1h10" {
		t.Errorf("preparation = %+v, want class edit, 38%% left, resets in 1h10", got)
	}
	if got.Suggestion.Action != windowshare.ActionNone {
		t.Errorf("suggested %q from a suppressed estimate", got.Suggestion.Action)
	}
	if !got.Recorded {
		t.Error("the start of the task was not recorded, so review could never measure it")
	}
}

func TestPrepareEstimatesFromTheSameClassAndScope(t *testing.T) {
	st := openStore(t)
	seedActuals(t, st, 10, 15, 20, 25, 30)
	meter(t, st, t0.Add(-time.Minute), 62, t0.Add(70*time.Minute))
	got, err := Prepare(context.Background(), Deps{Config: anthropicConfig(), Store: st}, PrepareRequest{
		WorkflowID: "workflow:new", Instruction: "fix the flaky timing test", Scope: "tokenops",
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	want := "typically 15–25% of your 5h window (p50 20%, p90 28%, n=5); 38% left, resets in 1h10"
	if got.Summary != want {
		t.Errorf("summary = %q, want %q", got.Summary, want)
	}
	if got.Estimate.Basis != windowshare.BasisClassAndScope || got.Suggestion.Action != windowshare.ActionProceed {
		t.Errorf("preparation = %+v, want a class-and-scope estimate that fits", got)
	}
}

func TestPrepareSuggestsALighterTierWhenOnlyTheLowEndFits(t *testing.T) {
	st := openStore(t)
	seedActuals(t, st, 10, 15, 20, 25, 30)
	meter(t, st, t0.Add(-time.Minute), 80, t0.Add(70*time.Minute))
	got, err := Prepare(context.Background(), Deps{Config: anthropicConfig(), Store: st}, PrepareRequest{
		WorkflowID: "workflow:new", Instruction: "fix the flaky timing test", Scope: "tokenops",
		Provider: "anthropic", Model: "claude-opus-4-1", SwitchTo: "claude-sonnet-4-5",
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Suggestion.Action != windowshare.ActionLighterTier {
		t.Fatalf("suggestion = %+v, want lighter_tier with 20%% left against 15–25%%", got.Suggestion)
	}
	if got.Suggestion.SuggestedTier == "" || got.Suggestion.SuggestedModel != "claude-sonnet-4-5" {
		t.Errorf("suggestion = %+v, want a tier below the current model and the routed model named", got.Suggestion)
	}
}

func TestPrepareIsUnavailableWithoutAWindowedPlan(t *testing.T) {
	st := openStore(t)
	got, err := Prepare(context.Background(), Deps{Config: &config.Config{}, Store: st}, PrepareRequest{
		WorkflowID: "workflow:new", Instruction: "rename the handler",
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusUnavailable || got.Recorded || got.Summary == "" {
		t.Errorf("preparation = %+v, want unavailable, explained and not recorded", got)
	}
}

func TestReviewRecordsTheActualAgainstTheEstimate(t *testing.T) {
	st := openStore(t)
	seedActuals(t, st, 10, 15, 20, 25, 30)
	reset := t0.Add(2 * time.Hour)
	meter(t, st, t0.Add(-time.Minute), 40, reset)
	deps := Deps{Config: anthropicConfig(), Store: st}
	if _, err := Prepare(context.Background(), deps, PrepareRequest{
		WorkflowID: "workflow:review", Instruction: "fix the flaky timing test", Scope: "tokenops",
	}, t0); err != nil {
		t.Fatal(err)
	}
	meter(t, st, t0.Add(30*time.Minute), 58, reset)

	got, err := Review(context.Background(), st, "workflow:review", t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("no review for a prepared workflow")
	}
	if !got.Actual.Measured || got.Actual.SharePct != 18 {
		t.Fatalf("actual = %+v, want 18 points measured", got.Actual)
	}
	if !got.Score.Scored || got.Score.Direction != windowshare.DirectionWithin || !got.Recorded {
		t.Errorf("review = %+v, want a recorded, scored, within-range result", got)
	}
	if !strings.Contains(got.Summary, "18%") || !strings.Contains(got.Summary, "15–25%") {
		t.Errorf("summary = %q, want the actual and the estimate", got.Summary)
	}

	// The measured task is now history for the next one, once, however
	// often it is reviewed.
	if _, err := Review(context.Background(), st, "workflow:review", t0.Add(time.Hour+time.Minute)); err != nil {
		t.Fatal(err)
	}
	hist, err := history(context.Background(), st, t0.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 6 {
		t.Errorf("history holds %d samples, want the 5 seeded plus this task once", len(hist))
	}
}

func TestReviewOfAnUnpreparedWorkflowIsNil(t *testing.T) {
	got, err := Review(context.Background(), openStore(t), "workflow:never", t0)
	if err != nil || got != nil {
		t.Fatalf("review = %+v, %v; want nil for a workflow never prepared", got, err)
	}
}

func TestPrepareNeverStoresTheInstruction(t *testing.T) {
	st := openStore(t)
	meter(t, st, t0.Add(-time.Minute), 10, t0.Add(time.Hour))
	const secret = "rotate the key sk-live-do-not-store"
	if _, err := Prepare(context.Background(), Deps{Config: anthropicConfig(), Store: st}, PrepareRequest{
		WorkflowID: "workflow:private", Instruction: secret,
	}, t0); err != nil {
		t.Fatal(err)
	}
	envs, err := st.Query(context.Background(), sqlite.Filter{Type: eventschema.EventTypeDomain, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range envs {
		if d, ok := env.Payload.(*eventschema.DomainEvent); ok && strings.Contains(string(d.Data), "sk-live") {
			t.Fatalf("the instruction was stored: %s", d.Data)
		}
	}
}

// The preparation carries the spread of the same kind's measured history:
// p50, p90 and n, beside the interquartile range it already offered.
func TestPrepareCarriesTheSpreadOfPastRuns(t *testing.T) {
	st := openStore(t)
	seedActuals(t, st, 2, 4, 5, 6, 8, 10, 12, 15, 30, 60)
	meter(t, st, t0.Add(-time.Minute), 62, t0.Add(70*time.Minute))
	got, err := Prepare(context.Background(), Deps{Config: anthropicConfig(), Store: st}, PrepareRequest{
		WorkflowID: "workflow:new", Instruction: "fix the flaky timing test", Scope: "tokenops",
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	sp := got.Spread
	if sp == nil || !sp.Measured() || sp.Samples != 10 || sp.P50Pct != 9 || sp.P90Pct != 33 {
		t.Fatalf("spread = %+v, want p50 9, p90 33 over 10 runs", sp)
	}
	if !strings.Contains(got.Summary, "p50 9%, p90 33%, n=10") {
		t.Errorf("summary %q does not carry the spread", got.Summary)
	}
}

// With fewer than five runs the spread says so and the answer stays the
// estimate it was before, labelled as the fallback.
func TestPrepareFallsBackBelowFiveRuns(t *testing.T) {
	st := openStore(t)
	seedActuals(t, st, 10, 20, 30)
	meter(t, st, t0.Add(-time.Minute), 62, t0.Add(70*time.Minute))
	got, err := Prepare(context.Background(), Deps{Config: anthropicConfig(), Store: st}, PrepareRequest{
		WorkflowID: "workflow:new", Instruction: "fix the flaky timing test", Scope: "tokenops",
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	sp := got.Spread
	if sp == nil || sp.Measured() || sp.Samples != 3 || sp.Fallback != windowshare.FallbackNone {
		t.Fatalf("spread = %+v, want too few (3) with nothing to fall back to", sp)
	}
	if strings.Contains(got.Summary, "p90") || !strings.Contains(got.Summary, "no estimate yet: 3 of 5") {
		t.Errorf("summary %q, want the earlier no-estimate answer", got.Summary)
	}

	got, err = Prepare(context.Background(), Deps{Config: anthropicConfig(), Store: st, MinSamples: 3}, PrepareRequest{
		WorkflowID: "workflow:new-2", Instruction: "fix the flaky timing test", Scope: "tokenops",
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Spread == nil || got.Spread.Fallback != windowshare.FallbackInterquartile || !got.Estimate.Estimated() {
		t.Fatalf("spread = %+v, want the interquartile estimate labelled as the fallback", got.Spread)
	}
}
