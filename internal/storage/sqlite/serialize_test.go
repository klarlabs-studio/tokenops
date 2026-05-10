package sqlite

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func fakeEnv(t eventschema.EventType, p eventschema.Payload) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID:            "test-id",
		SchemaVersion: "1.0.0",
		Type:          t,
		Timestamp:     time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC),
		Payload:       p,
	}
}

func TestEnvelopeToRowNil(t *testing.T) {
	_, err := envelopeToRow(nil)
	if err == nil {
		t.Fatal("expected error for nil envelope")
	}
}

func TestEnvelopeToRowEmptyID(t *testing.T) {
	env := fakeEnv(eventschema.EventTypePrompt, &eventschema.PromptEvent{})
	env.ID = ""
	_, err := envelopeToRow(env)
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestEnvelopeToRowNilPayload(t *testing.T) {
	env := fakeEnv(eventschema.EventTypePrompt, nil)
	env.Payload = nil
	_, err := envelopeToRow(env)
	if err == nil {
		t.Fatal("expected error for nil payload")
	}
}

func TestEnvelopeToRowTypeMismatch(t *testing.T) {
	env := fakeEnv(eventschema.EventTypePrompt, &eventschema.WorkflowEvent{})
	_, err := envelopeToRow(env)
	if err == nil {
		t.Fatal("expected error for type mismatch")
	}
}

func TestEnvelopeToRowPrompt(t *testing.T) {
	env := fakeEnv(eventschema.EventTypePrompt, &eventschema.PromptEvent{
		Provider:     eventschema.ProviderAnthropic,
		RequestModel: "claude-3-opus",
		InputTokens:  100,
		OutputTokens: 50,
		TotalTokens:  150,
		CostUSD:      0.015,
	})
	r, err := envelopeToRow(env)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "test-id" {
		t.Errorf("ID = %q", r.ID)
	}
	if r.Type != eventschema.EventTypePrompt {
		t.Errorf("Type = %q", r.Type)
	}
	if !r.Provider.Valid || r.Provider.String != string(eventschema.ProviderAnthropic) {
		t.Errorf("Provider = %+v, want %q", r.Provider, eventschema.ProviderAnthropic)
	}
	if !r.Model.Valid || r.Model.String != "claude-3-opus" {
		t.Errorf("Model = %+v", r.Model)
	}
	if !r.InputTokens.Valid || r.InputTokens.Int64 != 100 {
		t.Errorf("InputTokens = %+v", r.InputTokens)
	}
	if !r.OutputTokens.Valid || r.OutputTokens.Int64 != 50 {
		t.Errorf("OutputTokens = %+v", r.OutputTokens)
	}
	if !r.CostUSD.Valid || r.CostUSD.Float64 != 0.015 {
		t.Errorf("CostUSD = %+v", r.CostUSD)
	}
}

func TestEnvelopeToRowWorkflow(t *testing.T) {
	env := fakeEnv(eventschema.EventTypeWorkflow, &eventschema.WorkflowEvent{
		WorkflowID:             "wf-1",
		CumulativeInputTokens:  200,
		CumulativeOutputTokens: 100,
		CumulativeTotalTokens:  300,
		CumulativeCostUSD:      0.03,
	})
	r, err := envelopeToRow(env)
	if err != nil {
		t.Fatal(err)
	}
	if !r.WorkflowID.Valid || r.WorkflowID.String != "wf-1" {
		t.Errorf("WorkflowID = %+v", r.WorkflowID)
	}
	if !r.TotalTokens.Valid || r.TotalTokens.Int64 != 300 {
		t.Errorf("TotalTokens = %+v", r.TotalTokens)
	}
	if !r.CostUSD.Valid || r.CostUSD.Float64 != 0.03 {
		t.Errorf("CostUSD = %+v", r.CostUSD)
	}
}

func TestEnvelopeToRowOptimization(t *testing.T) {
	env := fakeEnv(eventschema.EventTypeOptimization, &eventschema.OptimizationEvent{
		WorkflowID: "wf-1",
	})
	r, err := envelopeToRow(env)
	if err != nil {
		t.Fatal(err)
	}
	if !r.WorkflowID.Valid || r.WorkflowID.String != "wf-1" {
		t.Errorf("WorkflowID = %+v", r.WorkflowID)
	}
	if r.InputTokens.Valid {
		t.Error("InputTokens should be null for optimization events")
	}
}

func TestEnvelopeToRowCoaching(t *testing.T) {
	env := fakeEnv(eventschema.EventTypeCoaching, &eventschema.CoachingEvent{
		WorkflowID: "wf-1",
		SessionID:  "sess-1",
	})
	r, err := envelopeToRow(env)
	if err != nil {
		t.Fatal(err)
	}
	if !r.WorkflowID.Valid || r.WorkflowID.String != "wf-1" {
		t.Errorf("WorkflowID = %+v", r.WorkflowID)
	}
	if !r.SessionID.Valid || r.SessionID.String != "sess-1" {
		t.Errorf("SessionID = %+v", r.SessionID)
	}
}

func TestEnvelopeToRowUnsupportedPayload(t *testing.T) {
	env := fakeEnv(eventschema.EventTypeUnknown, &unsupportedPayload{})
	_, err := envelopeToRow(env)
	if err == nil {
		t.Fatal("expected error for unsupported payload type")
	}
}

type unsupportedPayload struct{}

func (u *unsupportedPayload) Type() eventschema.EventType { return eventschema.EventTypeUnknown }

func TestRowToEnvelopeRoundTrip(t *testing.T) {
	env := fakeEnv(eventschema.EventTypePrompt, &eventschema.PromptEvent{
		Provider:     eventschema.ProviderOpenAI,
		RequestModel: "gpt-4",
		InputTokens:  100,
		OutputTokens: 50,
		TotalTokens:  150,
		CostUSD:      0.01,
	})
	r, err := envelopeToRow(env)
	if err != nil {
		t.Fatal(err)
	}
	got, err := rowToEnvelope(r)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != env.ID {
		t.Errorf("ID = %q, want %q", got.ID, env.ID)
	}
	if got.Type != env.Type {
		t.Errorf("Type = %q, want %q", got.Type, env.Type)
	}
	if !got.Timestamp.Equal(env.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, env.Timestamp)
	}
}

func TestDecodePayloadPrompt(t *testing.T) {
	raw := `{"provider":"OpenAI","request_model":"gpt-4"}`
	p, err := decodePayload(eventschema.EventTypePrompt, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*eventschema.PromptEvent); !ok {
		t.Errorf("type = %T, want *PromptEvent", p)
	}
}

func TestDecodePayloadWorkflow(t *testing.T) {
	raw := `{"workflow_id":"wf-1"}`
	p, err := decodePayload(eventschema.EventTypeWorkflow, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*eventschema.WorkflowEvent); !ok {
		t.Errorf("type = %T, want *WorkflowEvent", p)
	}
}

func TestDecodePayloadOptimization(t *testing.T) {
	raw := `{"kind":"prompt_compress"}`
	p, err := decodePayload(eventschema.EventTypeOptimization, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*eventschema.OptimizationEvent); !ok {
		t.Errorf("type = %T, want *OptimizationEvent", p)
	}
}

func TestDecodePayloadCoaching(t *testing.T) {
	raw := `{"workflow_id":"wf-1"}`
	p, err := decodePayload(eventschema.EventTypeCoaching, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*eventschema.CoachingEvent); !ok {
		t.Errorf("type = %T, want *CoachingEvent", p)
	}
}

func TestDecodePayloadUnknown(t *testing.T) {
	_, err := decodePayload("unknown_type", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown event type")
	}
}

func TestBuildDSNMemory(t *testing.T) {
	dsn, err := buildDSN(":memory:", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dsn, "file::memory:") {
		t.Errorf("buildDSN(:memory:) = %q, want file::memory: prefix", dsn)
	}
}

func TestBuildDSNPath(t *testing.T) {
	dsn, err := buildDSN("/tmp/test.db", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if dsn == "" {
		t.Fatal("expected non-empty DSN")
	}
}

func TestBuildDSNBusyTimeout(t *testing.T) {
	dsn, err := buildDSN(":memory:", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dsn, "5000") {
		t.Errorf("DSN missing busy_timeout(5000): %q", dsn)
	}
}

func TestRowToEnvelopeWithAttributes(t *testing.T) {
	r := row{
		ID:            "env-1",
		SchemaVersion: "1.0.0",
		Type:          eventschema.EventTypePrompt,
		TimestampNS:   time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC).UnixNano(),
		Payload:       `{"provider":"OpenAI","request_model":"gpt-4"}`,
		Attributes:    sql.NullString{String: `{"env":"test"}`, Valid: true},
	}
	env, err := rowToEnvelope(r)
	if err != nil {
		t.Fatal(err)
	}
	if env.ID != "env-1" {
		t.Errorf("ID = %q", env.ID)
	}
	if env.Attributes == nil {
		t.Fatal("Attributes is nil")
	}
	if env.Attributes["env"] != "test" {
		t.Errorf("Attributes[env] = %q", env.Attributes["env"])
	}
}
