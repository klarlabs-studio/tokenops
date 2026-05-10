// Package otlp ships TokenOps envelopes to an OTLP/HTTP/JSON-compatible
// collector (the OpenTelemetry Collector, Honeycomb, Grafana Agent, …).
// The exporter speaks the OTLP/HTTP wire format with JSON encoding so we
// avoid pulling in the full OpenTelemetry SDK + protobuf descriptors.
//
// Each TokenOps Envelope becomes one OTLP LogRecord; the typed payload's
// numeric fields (tokens, latency, cost, savings) become attributes
// keyed by the GenAI semantic conventions where applicable. PromptEvents
// also surface as histograms-friendly attributes so a downstream
// processor can convert them into metrics if desired.
//
// Redaction: when a Redactor is supplied, RedactEnvelope is invoked
// before encoding so secrets never leave the daemon.
package otlp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/redaction"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Options configures the Exporter.
type Options struct {
	// Endpoint is the base OTLP/HTTP URL of the collector
	// (e.g. "http://localhost:4318"). The exporter appends "/v1/logs".
	// Required; New returns an error when empty.
	Endpoint string
	// Headers are sent on every export request. Use this to attach a
	// bearer token or vendor-specific tenant header.
	Headers map[string]string
	// ServiceName is the resource attribute "service.name". Default "tokenops".
	ServiceName string
	// ServiceVersion is the resource attribute "service.version".
	ServiceVersion string
	// Client overrides the default 10-second-timeout HTTP client.
	Client *http.Client
	// Redactor is invoked on each envelope before encoding when non-nil.
	// Use nil only when redaction has already been applied upstream.
	Redactor *redaction.Redactor
	// Logger is used for non-fatal export failures. Default slog.Default().
	Logger *slog.Logger
}

// Exporter is an events.Sink implementation that ships envelopes to an
// OTLP/HTTP/JSON collector.
type Exporter struct {
	endpoint string
	headers  map[string]string
	resource resource
	scope    scope
	client   *http.Client
	redactor *redaction.Redactor
	logger   *slog.Logger

	exported atomic.Int64
	failed   atomic.Int64
}

// New builds an Exporter or returns an error when the configuration is
// not usable (empty endpoint, malformed headers).
func New(opts Options) (*Exporter, error) {
	if opts.Endpoint == "" {
		return nil, fmt.Errorf("otlp: endpoint must not be empty")
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 10 * time.Second}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	svc := opts.ServiceName
	if svc == "" {
		svc = "tokenops"
	}
	res := resource{Attributes: []kv{
		stringKV("service.name", svc),
	}}
	if opts.ServiceVersion != "" {
		res.Attributes = append(res.Attributes, stringKV("service.version", opts.ServiceVersion))
	}
	return &Exporter{
		endpoint: strings.TrimRight(opts.Endpoint, "/") + "/v1/logs",
		headers:  opts.Headers,
		resource: res,
		scope:    scope{Name: "tokenops"},
		client:   opts.Client,
		redactor: opts.Redactor,
		logger:   opts.Logger,
	}, nil
}

// AppendBatch implements events.Sink. Envelopes are translated to OTLP
// LogRecords and POSTed in a single request. Errors are logged but not
// returned because the bus contract treats Sink errors as transient.
func (e *Exporter) AppendBatch(ctx context.Context, envs []*eventschema.Envelope) error {
	if len(envs) == 0 {
		return nil
	}
	logRecords := make([]logRecord, 0, len(envs))
	for _, env := range envs {
		if env == nil {
			continue
		}
		if e.redactor != nil {
			_ = e.redactor.RedactEnvelope(env)
		}
		logRecords = append(logRecords, e.envelopeToLogRecord(env))
	}
	body := exportLogsRequest{
		ResourceLogs: []resourceLogs{{
			Resource: e.resource,
			ScopeLogs: []scopeLogs{{
				Scope:      e.scope,
				LogRecords: logRecords,
			}},
		}},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("otlp: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("otlp: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range e.headers {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.failed.Add(int64(len(logRecords)))
		e.logger.Warn("otlp: export failed", "err", err)
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		e.failed.Add(int64(len(logRecords)))
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		e.logger.Warn("otlp: export non-2xx",
			"status", resp.StatusCode,
			"body", string(body))
		return nil
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	e.exported.Add(int64(len(logRecords)))
	return nil
}

// ExportedCount returns the running total of LogRecords successfully sent.
func (e *Exporter) ExportedCount() int64 { return e.exported.Load() }

// FailedCount returns the running total of LogRecords the exporter
// declined to send (network or non-2xx response).
func (e *Exporter) FailedCount() int64 { return e.failed.Load() }

// --- envelope → OTLP translation --------------------------------------

func (e *Exporter) envelopeToLogRecord(env *eventschema.Envelope) logRecord {
	rec := logRecord{
		TimeUnixNano:         fmt.Sprintf("%d", env.Timestamp.UTC().UnixNano()),
		ObservedTimeUnixNano: fmt.Sprintf("%d", time.Now().UTC().UnixNano()),
		SeverityNumber:       severityForType(env.Type),
		SeverityText:         "INFO",
		Body:                 anyValue{StringValue: ptr(string(env.Type))},
		Attributes: []kv{
			stringKV(eventschema.AttrTokenOpsSchemaVersion, env.SchemaVersion),
			stringKV(eventschema.AttrTokenOpsEventType, string(env.Type)),
		},
	}
	if env.Source != "" {
		rec.Attributes = append(rec.Attributes, stringKV("tokenops.source", env.Source))
	}
	for k, v := range env.Attributes {
		rec.Attributes = append(rec.Attributes, stringKV(k, v))
	}
	switch p := env.Payload.(type) {
	case *eventschema.PromptEvent:
		rec.Attributes = append(rec.Attributes, promptAttributes(p)...)
	case *eventschema.WorkflowEvent:
		rec.Attributes = append(rec.Attributes, workflowAttributes(p)...)
	case *eventschema.OptimizationEvent:
		rec.Attributes = append(rec.Attributes, optimizationAttributes(p)...)
	case *eventschema.CoachingEvent:
		rec.Attributes = append(rec.Attributes, coachingAttributes(p)...)
	}
	return rec
}

func promptAttributes(p *eventschema.PromptEvent) []kv {
	out := []kv{
		stringKV(eventschema.AttrGenAISystem, eventschema.GenAISystem(p.Provider)),
		stringKV(eventschema.AttrGenAIRequestModel, p.RequestModel),
		intKV(eventschema.AttrGenAIUsageInputTokens, p.InputTokens),
		intKV(eventschema.AttrGenAIUsageOutputTokens, p.OutputTokens),
		intKV(eventschema.AttrGenAIUsageTotalTokens, p.TotalTokens),
		intKV(eventschema.AttrTokenOpsContextSize, p.ContextSize),
		intKV(eventschema.AttrTokenOpsLatencyNS, int64(p.Latency)),
		boolKV(eventschema.AttrTokenOpsStreaming, p.Streaming),
		boolKV(eventschema.AttrTokenOpsCacheHit, p.CacheHit),
		stringKV(eventschema.AttrTokenOpsPromptHash, p.PromptHash),
	}
	if p.ResponseModel != "" {
		out = append(out, stringKV(eventschema.AttrGenAIResponseModel, p.ResponseModel))
	}
	if p.MaxOutputTokens != 0 {
		out = append(out, intKV(eventschema.AttrGenAIRequestMaxTokens, p.MaxOutputTokens))
	}
	if p.TimeToFirstToken != 0 {
		out = append(out, intKV(eventschema.AttrTokenOpsTimeToFirstTokenNS, int64(p.TimeToFirstToken)))
	}
	if p.CachedInputTokens != 0 {
		out = append(out, intKV(eventschema.AttrTokenOpsCachedInputTokens, p.CachedInputTokens))
	}
	if p.CostUSD != 0 {
		out = append(out, doubleKV(eventschema.AttrTokenOpsCostUSD, p.CostUSD))
	}
	if p.WorkflowID != "" {
		out = append(out, stringKV(eventschema.AttrTokenOpsWorkflowID, p.WorkflowID))
	}
	if p.AgentID != "" {
		out = append(out, stringKV(eventschema.AttrTokenOpsAgentID, p.AgentID))
	}
	if p.SessionID != "" {
		out = append(out, stringKV(eventschema.AttrTokenOpsSessionID, p.SessionID))
	}
	if p.UserID != "" {
		out = append(out, stringKV(eventschema.AttrTokenOpsUserID, p.UserID))
	}
	if p.Status != 0 {
		out = append(out, intKV("http.response.status_code", int64(p.Status)))
	}
	if p.FinishReason != "" {
		out = append(out, stringKV(eventschema.AttrGenAIResponseFinish, p.FinishReason))
	}
	return out
}

func workflowAttributes(p *eventschema.WorkflowEvent) []kv {
	out := []kv{
		stringKV(eventschema.AttrTokenOpsWorkflowID, p.WorkflowID),
		stringKV(eventschema.AttrTokenOpsWorkflowState, string(p.State)),
		intKV(eventschema.AttrTokenOpsWorkflowStepCount, int64(p.StepCount)),
	}
	if p.AgentID != "" {
		out = append(out, stringKV(eventschema.AttrTokenOpsAgentID, p.AgentID))
	}
	if p.ParentWorkflowID != "" {
		out = append(out, stringKV(eventschema.AttrTokenOpsWorkflowParentID, p.ParentWorkflowID))
	}
	if p.ErrorCode != "" {
		out = append(out, stringKV("error.type", p.ErrorCode))
	}
	return out
}

func optimizationAttributes(p *eventschema.OptimizationEvent) []kv {
	return []kv{
		stringKV(eventschema.AttrTokenOpsOptimizationType, string(p.Kind)),
		stringKV(eventschema.AttrTokenOpsOptimizationMode, string(p.Mode)),
		stringKV(eventschema.AttrTokenOpsOptimizationDecision, string(p.Decision)),
		intKV(eventschema.AttrTokenOpsEstimatedSavings, p.EstimatedSavingsTokens),
		doubleKV(eventschema.AttrTokenOpsQualityScore, p.QualityScore),
		stringKV(eventschema.AttrTokenOpsPromptHash, p.PromptHash),
	}
}

func coachingAttributes(p *eventschema.CoachingEvent) []kv {
	out := []kv{
		stringKV(eventschema.AttrTokenOpsCoachingKind, string(p.Kind)),
		intKV(eventschema.AttrTokenOpsEstimatedSavings, p.EstimatedSavingsTokens),
	}
	if p.WorkflowID != "" {
		out = append(out, stringKV(eventschema.AttrTokenOpsWorkflowID, p.WorkflowID))
	}
	if p.AgentID != "" {
		out = append(out, stringKV(eventschema.AttrTokenOpsAgentID, p.AgentID))
	}
	if p.Summary != "" {
		out = append(out, stringKV("tokenops.coaching.summary", p.Summary))
	}
	return out
}

// severityForType maps a TokenOps event kind to a coarse OTLP severity.
// The exact mapping isn't load-bearing; collectors pivot on the event
// type attribute, not the severity.
func severityForType(t eventschema.EventType) int {
	switch t {
	case eventschema.EventTypeWorkflow:
		return 9 // INFO
	case eventschema.EventTypePrompt:
		return 9
	case eventschema.EventTypeOptimization:
		return 9
	case eventschema.EventTypeCoaching:
		return 11 // INFO2
	default:
		return 9
	}
}

// --- OTLP/HTTP JSON wire types ----------------------------------------

// These match the OTLP/HTTP/JSON encoding documented at
// https://opentelemetry.io/docs/specs/otlp/#otlphttp. Kept private so
// nothing outside this package binds to a particular schema version.

type exportLogsRequest struct {
	ResourceLogs []resourceLogs `json:"resourceLogs"`
}

type resourceLogs struct {
	Resource  resource    `json:"resource"`
	ScopeLogs []scopeLogs `json:"scopeLogs"`
}

type scopeLogs struct {
	Scope      scope       `json:"scope"`
	LogRecords []logRecord `json:"logRecords"`
}

type resource struct {
	Attributes []kv `json:"attributes,omitempty"`
}

type scope struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type logRecord struct {
	TimeUnixNano         string   `json:"timeUnixNano"`
	ObservedTimeUnixNano string   `json:"observedTimeUnixNano"`
	SeverityNumber       int      `json:"severityNumber"`
	SeverityText         string   `json:"severityText"`
	Body                 anyValue `json:"body"`
	Attributes           []kv     `json:"attributes,omitempty"`
}

type kv struct {
	Key   string   `json:"key"`
	Value anyValue `json:"value"`
}

type anyValue struct {
	StringValue *string  `json:"stringValue,omitempty"`
	BoolValue   *bool    `json:"boolValue,omitempty"`
	IntValue    *string  `json:"intValue,omitempty"`
	DoubleValue *float64 `json:"doubleValue,omitempty"`
}

func ptr[T any](v T) *T { return &v }

func stringKV(key, value string) kv {
	return kv{Key: key, Value: anyValue{StringValue: ptr(value)}}
}

func intKV(key string, value int64) kv {
	// OTLP int values travel as decimal strings to dodge JS-side int53
	// truncation. Honor that contract.
	s := fmt.Sprintf("%d", value)
	return kv{Key: key, Value: anyValue{IntValue: &s}}
}

func boolKV(key string, value bool) kv {
	return kv{Key: key, Value: anyValue{BoolValue: ptr(value)}}
}

func doubleKV(key string, value float64) kv {
	return kv{Key: key, Value: anyValue{DoubleValue: ptr(value)}}
}
