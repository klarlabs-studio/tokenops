package otlp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/felixgeelhaar/tokenops/internal/contexts/security/redaction"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// captureCollector is a minimal OTLP/HTTP/JSON receiver used by the
// exporter tests. It records every body POSTed to /v1/logs so assertions
// can inspect the on-wire payload.
type captureCollector struct {
	srv      *httptest.Server
	bodies   atomic.Pointer[[]byte]
	calls    atomic.Int64
	respCode atomic.Int32
}

func newCaptureCollector(t *testing.T) *captureCollector {
	t.Helper()
	c := &captureCollector{}
	c.respCode.Store(200)
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		c.bodies.Store(&body)
		c.calls.Add(1)
		w.WriteHeader(int(c.respCode.Load()))
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func samplePromptEnvelope() *eventschema.Envelope {
	return &eventschema.Envelope{
		ID:            uuid.NewString(),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     time.Now().UTC(),
		Source:        "proxy",
		Payload: &eventschema.PromptEvent{
			PromptHash:   "sha256:abc",
			Provider:     eventschema.ProviderOpenAI,
			RequestModel: "gpt-4o-mini",
			InputTokens:  100,
			OutputTokens: 20,
			TotalTokens:  120,
			ContextSize:  100,
			Latency:      250 * time.Millisecond,
			Streaming:    false,
			Status:       200,
			CostUSD:      0.0042,
			WorkflowID:   "wf-1",
			AgentID:      "planner",
		},
	}
}

func TestExporterEmitsPromptEnvelope(t *testing.T) {
	collector := newCaptureCollector(t)
	exp, err := New(Options{Endpoint: collector.srv.URL, ServiceVersion: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := exp.AppendBatch(context.Background(), []*eventschema.Envelope{samplePromptEnvelope()}); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}

	if collector.calls.Load() != 1 {
		t.Fatalf("calls = %d", collector.calls.Load())
	}
	if exp.ExportedCount() != 1 {
		t.Errorf("exported = %d", exp.ExportedCount())
	}

	bodyPtr := collector.bodies.Load()
	if bodyPtr == nil {
		t.Fatal("collector saw no body")
	}
	var got map[string]any
	if err := json.Unmarshal(*bodyPtr, &got); err != nil {
		t.Fatalf("not json: %v\n%s", err, string(*bodyPtr))
	}

	// Walk to the LogRecord attributes.
	rls := got["resourceLogs"].([]any)
	if len(rls) != 1 {
		t.Fatalf("resourceLogs = %d", len(rls))
	}
	logBytes, _ := json.Marshal(rls[0])
	logStr := string(logBytes)
	for _, want := range []string{
		`"service.name"`,
		`"gen_ai.system"`,
		`"openai"`,
		`"gen_ai.usage.input_tokens"`,
		`"100"`, // OTLP encodes ints as decimal strings
		`"tokenops.workflow.id"`,
		`"wf-1"`,
		`"tokenops.cost_usd"`,
	} {
		if !strings.Contains(logStr, want) {
			t.Errorf("missing %q in payload:\n%s", want, logStr)
		}
	}
}

func TestExporterEmitsRuleEnvelopes(t *testing.T) {
	collector := newCaptureCollector(t)
	exp, err := New(Options{Endpoint: collector.srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	src := &eventschema.Envelope{
		ID:            uuid.NewString(),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypeRuleSource,
		Timestamp:     time.Now().UTC(),
		Source:        "rule-engine",
		Payload: &eventschema.RuleSourceEvent{
			SourceID:    "repo:CLAUDE.md",
			Source:      eventschema.RuleSourceClaudeMD,
			Scope:       eventschema.RuleScopeRepo,
			Path:        "CLAUDE.md",
			RepoID:      "repo",
			Tokenizer:   "openai/cl100k_base",
			Provider:    eventschema.ProviderOpenAI,
			TotalTokens: 1200,
			TotalChars:  4800,
			Hash:        "sha256:deadbeef",
			Sections: []eventschema.RuleSection{
				{ID: "repo:CLAUDE.md#Testing", Anchor: "Testing", TokenCount: 200},
			},
		},
	}
	ana := &eventschema.Envelope{
		ID:            uuid.NewString(),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypeRuleAnalysis,
		Timestamp:     time.Now().UTC(),
		Source:        "rule-engine",
		Payload: &eventschema.RuleAnalysisEvent{
			SourceID:      "repo:CLAUDE.md",
			SectionID:     "repo:CLAUDE.md#Testing",
			WindowStart:   time.Now().UTC().Add(-time.Hour),
			WindowEnd:     time.Now().UTC(),
			Exposures:     50,
			ContextTokens: 10000,
			TokensSaved:   1500,
			ROIScore:      0.42,
		},
	}
	if err := exp.AppendBatch(context.Background(), []*eventschema.Envelope{src, ana}); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}
	bodyPtr := collector.bodies.Load()
	if bodyPtr == nil {
		t.Fatal("collector saw no body")
	}
	got := string(*bodyPtr)
	for _, want := range []string{
		`"tokenops.rule.source_id"`,
		`"repo:CLAUDE.md"`,
		`"tokenops.rule.source"`,
		`"claude_md"`,
		`"tokenops.rule.total_tokens"`,
		`"tokenops.rule.section_count"`,
		`"tokenops.rule.exposures"`,
		`"tokenops.rule.context_tokens"`,
		`"tokenops.rule.roi_score"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in payload:\n%s", want, got)
		}
	}
}

func TestExporterRedactsBeforeSend(t *testing.T) {
	collector := newCaptureCollector(t)
	red := redaction.New(redaction.Config{})
	exp, err := New(Options{Endpoint: collector.srv.URL, Redactor: red})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	env := samplePromptEnvelope()
	pe := env.Payload.(*eventschema.PromptEvent)
	pe.AgentID = "Bearer sk-secretsecretsecretsecretsecret123"

	if err := exp.AppendBatch(context.Background(), []*eventschema.Envelope{env}); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}
	body := *collector.bodies.Load()
	if strings.Contains(string(body), "sk-secretsecretsecretsecretsecret123") {
		t.Errorf("secret leaked into OTLP payload:\n%s", body)
	}
}

func TestExporterReportsFailedOnNon2xx(t *testing.T) {
	collector := newCaptureCollector(t)
	collector.respCode.Store(503)
	exp, err := New(Options{Endpoint: collector.srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := exp.AppendBatch(context.Background(), []*eventschema.Envelope{samplePromptEnvelope()}); err != nil {
		t.Fatalf("AppendBatch returned err: %v", err)
	}
	if exp.FailedCount() != 1 {
		t.Errorf("failed count = %d", exp.FailedCount())
	}
	if exp.ExportedCount() != 0 {
		t.Errorf("exported count = %d, want 0", exp.ExportedCount())
	}
}

func TestExporterEmptyEndpointError(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected error for empty endpoint")
	}
}

func TestExporterAppliesCustomHeaders(t *testing.T) {
	var gotHeader atomic.Pointer[string]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := r.Header.Get("X-Honeycomb-Team")
		gotHeader.Store(&v)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	exp, err := New(Options{
		Endpoint: srv.URL,
		Headers:  map[string]string{"X-Honeycomb-Team": "test-team"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = exp.AppendBatch(context.Background(), []*eventschema.Envelope{samplePromptEnvelope()})
	got := gotHeader.Load()
	if got == nil || *got != "test-team" {
		t.Errorf("custom header lost: %v", got)
	}
}
