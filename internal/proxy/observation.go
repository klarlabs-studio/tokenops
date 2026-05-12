package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/providers"
	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/tokenizer"
	"github.com/felixgeelhaar/tokenops/internal/events"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Attribution headers — clients tag requests with workflow / agent / session
// IDs so the proxy can stitch related calls together. Header names are
// case-insensitive per HTTP, but we declare the canonical form here so
// log output and docs stay consistent.
const (
	headerWorkflowID = "X-Tokenops-Workflow-Id"
	headerAgentID    = "X-Tokenops-Agent-Id"
	headerSessionID  = "X-Tokenops-Session-Id"
	headerUserID     = "X-Tokenops-User-Id"
)

// maxRequestBodyCapture caps the bytes the observer reads from a request
// body. 4 MB is well above the largest payloads observed in practice and
// guards against memory pressure on pathological clients.
const maxRequestBodyCapture = 4 * 1024 * 1024

// maxResponseBodyCapture caps the bytes the observer retains for output
// token estimation. Streams beyond this length still pass through the
// proxy untouched; only the token estimate stops growing.
const maxResponseBodyCapture = 4 * 1024 * 1024

// observationKey is the unexported context key under which the active
// requestObservation lives. Using a private struct{} type avoids
// collisions with caller-installed context values.
type observationKey struct{}

// requestObservation is the per-request state assembled by the observer
// middleware and finalised by the StreamMeter when the response body
// closes. It is owned by exactly one request goroutine.
type requestObservation struct {
	Start    time.Time
	Provider eventschema.Provider
	Prefix   string

	PromptHash    string
	RequestModel  string
	InputTokens   int64
	ContextSize   int64
	MessageCount  int
	SystemPresent bool
	Streaming     bool
	MaxOutput     int64

	WorkflowID string
	AgentID    string
	SessionID  string
	UserID     string

	Status        int
	ResponseModel string
	FirstByteAt   atomic.Int64 // unix nanos; 0 until first byte
	captured      bytes.Buffer
	captureMu     sync.Mutex
	captureLimit  int
}

// observerMeter is the StreamMeter wired into the proxy when an event bus
// is configured. NewMeter runs once per upstream response and returns a
// per-stream meter that captures bytes for token estimation and emits a
// PromptEvent on Done.
type observerMeter struct {
	bus       events.Bus
	tokenizer *tokenizer.Registry
	source    string
}

// NewMeter implements StreamMeter.
func (m *observerMeter) NewMeter(resp *http.Response) RequestMeter {
	obs, _ := resp.Request.Context().Value(observationKey{}).(*requestObservation)
	if obs == nil {
		// No observation in context — request bypassed the observer
		// middleware (e.g. /healthz). Return a noop meter so the proxy
		// still works without emitting events.
		return noopRequestMeter{}
	}
	obs.captureLimit = maxResponseBodyCapture
	obs.Status = resp.StatusCode
	obs.ResponseModel = extractResponseModel(resp)
	obs.Streaming = isStreamingResponse(resp)
	return &observerRequestMeter{obs: obs, m: m}
}

type observerRequestMeter struct {
	obs *requestObservation
	m   *observerMeter
}

func (r *observerRequestMeter) Observe(chunk []byte) {
	if r.obs.FirstByteAt.Load() == 0 {
		r.obs.FirstByteAt.CompareAndSwap(0, time.Now().UTC().UnixNano())
	}
	r.obs.captureMu.Lock()
	if r.obs.captured.Len() < r.obs.captureLimit {
		remaining := r.obs.captureLimit - r.obs.captured.Len()
		if len(chunk) > remaining {
			r.obs.captured.Write(chunk[:remaining])
		} else {
			r.obs.captured.Write(chunk)
		}
	}
	r.obs.captureMu.Unlock()
}

func (r *observerRequestMeter) Done(_ int64) {
	end := time.Now().UTC()
	latency := end.Sub(r.obs.Start)

	r.obs.captureMu.Lock()
	body := append([]byte(nil), r.obs.captured.Bytes()...)
	r.obs.captureMu.Unlock()

	outputTokens := int64(0)
	if r.m.tokenizer != nil {
		if n, err := r.m.tokenizer.CountText(r.obs.Provider, string(body)); err == nil {
			outputTokens = int64(n)
		}
	}

	ttft := time.Duration(0)
	if first := r.obs.FirstByteAt.Load(); first > 0 {
		ttft = time.Unix(0, first).Sub(r.obs.Start)
	}

	env := &eventschema.Envelope{
		ID:            uuid.NewString(),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     r.obs.Start.UTC(),
		Source:        r.m.source,
		Payload: &eventschema.PromptEvent{
			PromptHash:       r.obs.PromptHash,
			Provider:         r.obs.Provider,
			RequestModel:     r.obs.RequestModel,
			ResponseModel:    r.obs.ResponseModel,
			InputTokens:      r.obs.InputTokens,
			OutputTokens:     outputTokens,
			TotalTokens:      r.obs.InputTokens + outputTokens,
			ContextSize:      r.obs.ContextSize,
			MaxOutputTokens:  r.obs.MaxOutput,
			Latency:          latency,
			TimeToFirstToken: ttft,
			Streaming:        r.obs.Streaming,
			Status:           r.obs.Status,
			WorkflowID:       r.obs.WorkflowID,
			AgentID:          r.obs.AgentID,
			SessionID:        r.obs.SessionID,
			UserID:           r.obs.UserID,
		},
	}
	r.m.bus.Publish(env)
}

// extractResponseModel reads the response model when the upstream reports
// it via header. Provider responses also frequently include the model in
// the JSON body; that path is not parsed here to avoid double-buffering.
func extractResponseModel(resp *http.Response) string {
	for _, h := range []string{"X-Model", "Openai-Model", "Anthropic-Model"} {
		if v := resp.Header.Get(h); v != "" {
			return v
		}
	}
	return ""
}

// captureRequestBody reads (up to maxRequestBodyCapture bytes from) r.Body,
// re-attaches a fresh reader so downstream handlers see the same bytes,
// and returns the captured slice. ContentLength is fixed up so the
// upstream sees an accurate length when we did not truncate.
func captureRequestBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodyCapture+1))
	_ = r.Body.Close()
	if err != nil {
		return nil, err
	}
	truncated := len(body) > maxRequestBodyCapture
	if truncated {
		body = body[:maxRequestBodyCapture]
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if !truncated {
		r.ContentLength = int64(len(body))
		r.Header.Set("Content-Length", strconv.Itoa(len(body)))
	}
	return body, nil
}

// observerMiddleware is the http.Handler wrapper installed in front of
// each provider's ReverseProxy when a Bus is configured. It captures the
// request body, hashes + canonicalises it, builds a requestObservation,
// and stashes it in the request context for ModifyResponse + the meter.
func (s *Server) observerMiddleware(provider providers.Provider, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := captureRequestBody(r)
		if err != nil {
			s.logger.Warn("capture request body", "err", err, "provider", provider.ID)
			next.ServeHTTP(w, r)
			return
		}

		obs := &requestObservation{
			Start:    time.Now().UTC(),
			Provider: provider.ID,
			Prefix:   provider.Prefix,

			WorkflowID: r.Header.Get(headerWorkflowID),
			AgentID:    r.Header.Get(headerAgentID),
			SessionID:  r.Header.Get(headerSessionID),
			UserID:     r.Header.Get(headerUserID),
		}
		if len(body) > 0 {
			sum := sha256.Sum256(body)
			obs.PromptHash = "sha256:" + hex.EncodeToString(sum[:])

			if provider.Normalize != nil {
				rest := strings.TrimPrefix(r.URL.Path, strings.TrimSuffix(provider.Prefix, "/"))
				if canonical, err := provider.Normalize(rest, body); err == nil {
					obs.RequestModel = canonical.Model
					obs.MessageCount = canonical.MessageCount
					obs.SystemPresent = canonical.SystemPresent
					obs.MaxOutput = canonical.MaxOutputTokens
				}
			}
			if s.tokenizer != nil {
				if n, err := s.tokenizer.PreflightCount(provider.ID, body); err == nil {
					obs.InputTokens = int64(n)
					obs.ContextSize = int64(n)
				}
			}
		}

		ctx := context.WithValue(r.Context(), observationKey{}, obs)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
