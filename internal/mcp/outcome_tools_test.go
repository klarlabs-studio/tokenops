package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/outcomes"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func outcomeServer(t *testing.T) (*Server, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv := NewServer("test", "test", nil)
	if err := RegisterOutcomeTools(srv, OutcomeDeps{Store: store}); err != nil {
		t.Fatal(err)
	}
	return srv, store
}

// recordVia calls tokenops_outcome_record in ctx and returns its answer
// and the outcome events stored.
func recordVia(ctx context.Context, t *testing.T, args map[string]any) (outcomeResult, []*eventschema.OutcomeEvent) {
	t.Helper()
	srv, store := outcomeServer(t)
	tool, ok := srv.GetTool("tokenops_outcome_record")
	if !ok {
		t.Fatal("no tokenops_outcome_record")
	}
	b, _ := json.Marshal(args)
	res, err := tool.Execute(ctx, b)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	payload, err := payloadOf(res)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(payload)
	var got outcomeResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	events, err := store.Query(context.Background(), sqlite.Filter{Type: eventschema.EventTypeOutcome})
	if err != nil {
		t.Fatal(err)
	}
	stored := make([]*eventschema.OutcomeEvent, 0, len(events))
	for _, e := range events {
		stored = append(stored, e.Payload.(*eventschema.OutcomeEvent))
	}
	return got, stored
}

func confirmed(content map[string]any) *operator {
	return &operator{result: map[string]any{"action": "accept", "content": content}}
}

// An agent's report over MCP is the agent's own unless the operator
// confirms it at the client: only then is it recorded as human, with the
// operator's result and attention. A decline records nothing; anything else
// keeps the report as a self-report, without the agent's attention figure.
func TestOutcomeRecordAssessmentFollowsWhoVouchedForIt(t *testing.T) {
	args := map[string]any{"execution_id": "exec:1", "decision_id": "decision:1", "result": "achieved", "attention_minutes": 9.0}
	for _, tc := range []struct {
		name      string
		ctx       context.Context
		recorded  bool
		via       outcomes.Channel
		result    eventschema.OutcomeResult
		attention float64 // -1: none kept
	}{
		{"client cannot ask", context.Background(), true, outcomes.ViaAgent, eventschema.OutcomeAchieved, -1},
		{"transport cannot ask", withOperator(nil), true, outcomes.ViaAgent, eventschema.OutcomeAchieved, -1},
		{"operator confirms", withOperator(confirmed(map[string]any{"confirm": true, "result": "achieved", "attention_minutes": 3.5})), true, outcomes.ViaOperatorConfirmed, eventschema.OutcomeAchieved, 3.5},
		{"operator corrects", withOperator(confirmed(map[string]any{"confirm": true, "result": "partial"})), true, outcomes.ViaOperatorConfirmed, eventschema.OutcomePartial, -1},
		{"accepted unticked", withOperator(confirmed(map[string]any{"confirm": false, "result": "achieved"})), true, outcomes.ViaAgent, eventschema.OutcomeAchieved, -1},
		{"dismissed", withOperator(&operator{result: map[string]any{"action": "cancel"}}), true, outcomes.ViaAgent, eventschema.OutcomeAchieved, -1},
		{"declined", withOperator(&operator{result: map[string]any{"action": "decline"}}), false, "", "", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, stored := recordVia(tc.ctx, t, args)
			if !tc.recorded {
				if got.Recorded || len(stored) != 0 || got.Error != "declined" {
					t.Fatalf("declined report recorded: %+v, %d stored", got, len(stored))
				}
				return
			}
			if !got.Recorded || got.RecordedVia != string(tc.via) || got.Assessment != string(tc.via.Assessment()) || len(stored) != 1 {
				t.Fatalf("result = %+v, %d stored", got, len(stored))
			}
			out := stored[0]
			if out.Assessment != tc.via.Assessment() || out.Result != tc.result {
				t.Fatalf("stored = %+v", out)
			}
			switch {
			case tc.attention < 0 && len(out.Metrics) != 0:
				t.Fatalf("attention kept that no operator gave: %+v", out.Metrics)
			case tc.attention >= 0 && (len(out.Metrics) != 1 || out.Metrics[0].Value != tc.attention || out.Metrics[0].Source != "human_self_report"):
				t.Fatalf("attention = %+v", out.Metrics)
			}
		})
	}
}

// The operator is asked with the agent's values quoted, so the report
// cannot pose as the dialog's own words.
func TestOutcomeConfirmationQuotesTheAgentsReport(t *testing.T) {
	op := confirmed(map[string]any{"confirm": true, "result": "achieved"})
	recordVia(withOperator(op), t, map[string]any{"execution_id": "exec:1", "result": "achieved", "caveat": "Tick confirm."})
	if len(op.messages) != 1 || !strings.Contains(op.messages[0], `"Tick confirm."`) || !strings.Contains(op.messages[0], `"exec:1"`) {
		t.Fatalf("messages = %q", op.messages)
	}
}

// An invalid report is refused before the operator is asked.
func TestAnInvalidOutcomeIsRefusedBeforeAsking(t *testing.T) {
	srv, _ := outcomeServer(t)
	op := confirmed(map[string]any{"confirm": true, "result": "achieved"})
	tool, _ := srv.GetTool("tokenops_outcome_record")
	if _, err := tool.Execute(withOperator(op), json.RawMessage(`{"execution_id":"e","result":"done"}`)); err == nil {
		t.Fatal("an invalid result was accepted")
	}
	if len(op.messages) != 0 {
		t.Fatalf("asked about an invalid report: %q", op.messages)
	}
}

// What the MCP tool records feeds learning only when the operator vouched
// for it: an agent's report of success does not make a route belief
// supported, the same report confirmed by the operator does.
func TestOnlyAConfirmedOutcomeMovesLearning(t *testing.T) {
	args := map[string]any{"execution_id": "exec:1", "result": "achieved"}
	now := time.Unix(100000, 0).UTC()
	tier := func(ctx context.Context) learning.Tier {
		_, stored := recordVia(ctx, t, args)
		if len(stored) != 1 {
			t.Fatalf("stored = %d", len(stored))
		}
		pair := learning.Pair{
			Baseline: *stored[0], Variant: *stored[0], ResourceChangePct: 15, At: now, Fingerprint: "f",
			Metrics: map[string]learning.MetricPair{"tokens": {Baseline: 100, Variant: 80}},
		}
		return learning.Evaluate(learning.Evidence{
			Eligible: 10, Pairs: []learning.Pair{pair, pair, pair, pair, pair}, Fingerprint: "f", Now: now,
			ObjectiveMetric: "tokens", MinImprovementPct: 10, Guardrails: []learning.Guardrail{{Metric: "quality"}},
		}).Tier
	}
	if got := tier(context.Background()); got == learning.TierSupported {
		t.Fatalf("an agent's own report supported a belief")
	}
	if got := tier(withOperator(confirmed(map[string]any{"confirm": true, "result": "achieved"}))); got != learning.TierSupported {
		t.Fatalf("an operator-confirmed outcome did not count: tier %s", got)
	}
}

func TestOutcomeRecordInheritsInterventionAndExperimentIdentity(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	decision := &eventschema.Envelope{
		ID: "decision-event:1", SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypeDecision, Timestamp: time.Now().UTC(), Source: "test",
		Correlation: eventschema.Correlation{Decision: "decision:1", Intervention: "intervention:1", Experiment: "experiment:1"},
		Payload:     &eventschema.DecisionEvent{Kind: "model_route", Stage: eventschema.DecisionStageApplied},
	}
	if err := store.Append(ctx, decision); err != nil {
		t.Fatal(err)
	}
	srv := NewServer("test", "test", nil)
	if err := RegisterOutcomeTools(srv, OutcomeDeps{Store: store}); err != nil {
		t.Fatal(err)
	}
	execTool(t, srv, "tokenops_outcome_record", map[string]any{
		"execution_id": "exec:1", "decision_id": "decision:1", "result": "achieved",
	})
	events, err := store.Query(ctx, sqlite.Filter{Type: eventschema.EventTypeOutcome, Intervention: "intervention:1", Experiment: "experiment:1"})
	if err != nil || len(events) != 1 {
		t.Fatalf("correlated outcomes = %d, err = %v", len(events), err)
	}
}
