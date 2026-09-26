package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/tokenops/internal/contexts/prompts/providers"
	"go.klarlabs.de/tokenops/internal/contexts/prompts/tokenizer"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Attribution headers — clients tag requests with workflow / agent / session
// IDs so the proxy can stitch related calls together. Header names are
// case-insensitive per HTTP, but we declare the canonical form here so
// log output and docs stay consistent.
const (
	headerWorkflowID  = "X-Tokenops-Workflow-Id"
	headerAgentID     = "X-Tokenops-Agent-Id"
	headerSessionID   = "X-Tokenops-Session-Id"
	headerUserID      = "X-Tokenops-User-Id"
	headerExecutionID = "X-Tokenops-Execution-Id"
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
	// CostSource is how this request will be billed — resolved once at
	// observation time from the server's plan coverage, then carried to
	// both the emitted PromptEvent and any cost-aware optimizer.
	CostSource eventschema.CostSource

	PromptHash   string
	RequestModel string
	RoutedModel  string
	// Compatibility traits retain only request structure, never values or
	// content. They let an opaque provider 400 remain diagnostically useful.
	ManualThinking    bool
	SamplingParameter bool
	InputTokens       int64
	// InputCounted reports whether InputTokens is a count or a
	// stand-in zero. A tokenizer that is absent, or that refuses this
	// provider, leaves the field at 0, which is indistinguishable
	// downstream from a request that carried no input.
	InputCounted  bool
	ContextSize   int64
	MessageCount  int
	SystemPresent bool
	Streaming     bool
	MaxOutput     int64

	WorkflowID  string
	AgentID     string
	SessionID   string
	UserID      string
	ExecutionID string
	Correlation eventschema.Correlation

	Status        int
	ResponseModel string
	// ResponseEncoding is used only to decode the observer's bounded copy.
	// The bytes forwarded to the client remain untouched.
	ResponseEncoding string
	FirstByteAt      atomic.Int64 // unix nanos; 0 until first byte
	captured         bytes.Buffer
	captureMu        sync.Mutex
	captureLimit     int
}

// observerMeter is the StreamMeter wired into the proxy when an event bus
// is configured. NewMeter runs once per upstream response and returns a
// per-stream meter that captures bytes for token estimation and emits a
// PromptEvent on Done.
type observerMeter struct {
	bus        events.Bus
	tokenizer  *tokenizer.Registry
	costEngine *spend.Engine
	source     string
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
	obs.ResponseEncoding = strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
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
	body = decodeObservedBody(body, r.obs.ResponseEncoding)

	// Counted until something says otherwise. A tokenizer that is absent,
	// or that refuses this provider, leaves the counts at zero — and a
	// zero nobody produced reads downstream exactly like a request that
	// consumed nothing. TokenSource is what tells them apart.
	outputTokens := int64(0)
	tokenSource := eventschema.TokenSourceCounted
	usage, usageReported := parseResponseUsage(body)
	if usageReported {
		r.obs.InputTokens = usage.InputTokens
		r.obs.InputCounted = true
		outputTokens = usage.OutputTokens
		tokenSource = eventschema.TokenSourceVendorReported
		if usage.Model != "" {
			r.obs.ResponseModel = usage.Model
		}
	} else if r.m.tokenizer == nil {
		tokenSource = eventschema.TokenSourceUncounted
	} else if n, err := r.m.tokenizer.CountText(r.obs.Provider, string(body)); err == nil {
		outputTokens = int64(n)
	} else {
		tokenSource = eventschema.TokenSourceUncounted
	}
	// The request side is counted by the middleware, which records
	// whether it managed to. Either half being uncounted makes the
	// event's totals uncounted: TotalTokens is their sum.
	if !r.obs.InputCounted {
		tokenSource = eventschema.TokenSourceUncounted
	}

	ttft := time.Duration(0)
	if first := r.obs.FirstByteAt.Load(); first > 0 {
		ttft = time.Unix(0, first).Sub(r.obs.Start)
	}

	prompt := &eventschema.PromptEvent{
		PromptHash:        r.obs.PromptHash,
		Provider:          r.obs.Provider,
		RequestModel:      r.obs.RequestModel,
		ResponseModel:     r.obs.ResponseModel,
		InputTokens:       r.obs.InputTokens,
		OutputTokens:      outputTokens,
		TotalTokens:       r.obs.InputTokens + outputTokens,
		CachedInputTokens: usage.CachedInputTokens,
		TokenSource:       tokenSource,
		ContextSize:       r.obs.ContextSize,
		MaxOutputTokens:   r.obs.MaxOutput,
		Latency:           latency,
		TimeToFirstToken:  ttft,
		Streaming:         r.obs.Streaming,
		Status:            r.obs.Status,
		FinishReason:      usage.FinishReason,
		ErrorCode: classifyUpstreamError(r.obs.Provider, r.obs.Status, body,
			r.obs.RoutedModel, r.obs.ManualThinking, r.obs.SamplingParameter),
		ToolCallCount: usage.ToolCallCount,
		CostSource:    r.obs.CostSource,
		WorkflowID:    r.obs.WorkflowID,
		AgentID:       r.obs.AgentID,
		SessionID:     r.obs.SessionID,
		UserID:        r.obs.UserID,
	}
	if r.m.costEngine != nil && prompt.CostSource == eventschema.CostSourceMetered && prompt.TokensCounted() {
		if cost, err := r.m.costEngine.ComputeAt(prompt, r.obs.Start); err == nil {
			prompt.CostUSD = cost
			prompt.CostMeasured = true
		}
	}
	env := &eventschema.Envelope{
		ID:            uuid.NewString(),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     r.obs.Start.UTC(),
		Source:        r.m.source,
		Association:   associationFor(r.obs),
		Correlation:   r.obs.Correlation,
		Payload:       prompt,
	}
	r.m.bus.Publish(env)
}

func decodeObservedBody(body []byte, encoding string) []byte {
	if !strings.Contains(encoding, "gzip") || len(body) == 0 {
		return body
	}
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return body
	}
	decompressed, err := io.ReadAll(io.LimitReader(reader, maxResponseBodyCapture+1))
	closeErr := reader.Close()
	if err != nil || closeErr != nil || len(decompressed) > maxResponseBodyCapture {
		return body
	}
	return decompressed
}

// classifyUpstreamError turns a provider error response into a bounded,
// content-free category. Provider messages are useful for diagnosis but may
// echo request content, so neither the raw body nor the message is retained.
func classifyUpstreamError(provider eventschema.Provider, status int, body []byte, routedModel string, manualThinking, samplingParameter bool) string {
	if status < http.StatusBadRequest {
		return ""
	}

	response := parseUpstreamErrorEnvelope(body)

	errorType := response.Error.Type
	if errorType == "" {
		errorType = response.Error.Code
	}
	message := strings.ToLower(response.Error.Message)
	if message == "" {
		message = strings.ToLower(response.Message)
	}

	if provider == eventschema.ProviderAnthropic {
		if errorType == "invalid_request_error" {
			switch {
			case containsAny(message, "temperature", "top_p", "top_k") &&
				containsAny(message, "unsupported", "not supported", "non-default", "not accepted"):
				return "anthropic.invalid_request.unsupported_sampling_parameter"
			case strings.Contains(message, "thinking") &&
				containsAny(message, "budget_tokens", "adaptive", "unsupported", "not supported", "not accepted"):
				return "anthropic.invalid_request.incompatible_thinking"
			case strings.Contains(message, "assistant") && strings.Contains(message, "prefill"):
				return "anthropic.invalid_request.unsupported_assistant_prefill"
			default:
				return "anthropic.invalid_request"
			}
		}
		if knownAnthropicErrorType(errorType) {
			return "anthropic." + strings.TrimSuffix(errorType, "_error")
		}
		if status == http.StatusBadRequest && strings.HasPrefix(routedModel, "claude-sonnet-5") {
			switch {
			case manualThinking:
				return "anthropic.http_400.with_manual_thinking"
			case samplingParameter:
				return "anthropic.http_400.with_sampling_parameter"
			default:
				// The upstream body may be deliberately opaque (or emitted by an
				// intermediary). Preserve the one fact TokenOps can prove without
				// retaining request content: the failure followed a model rewrite.
				return "anthropic.http_400.after_model_route"
			}
		}
		return "anthropic.http_" + strconv.Itoa(status)
	}

	return string(provider) + ".http_" + strconv.Itoa(status)
}

type upstreamErrorEnvelope struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Error   struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func parseUpstreamErrorEnvelope(body []byte) upstreamErrorEnvelope {
	var response upstreamErrorEnvelope
	if json.Unmarshal(body, &response) == nil && (response.Error.Type != "" || response.Error.Code != "") {
		return response
	}
	for _, frame := range bytes.Split(bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n")), []byte("\n\n")) {
		for _, line := range bytes.Split(frame, []byte("\n")) {
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if json.Unmarshal(data, &response) == nil && (response.Error.Type != "" || response.Error.Code != "") {
				return response
			}
		}
	}
	return upstreamErrorEnvelope{}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func knownAnthropicErrorType(value string) bool {
	switch value {
	case "authentication_error", "permission_error", "not_found_error",
		"request_too_large", "rate_limit_error", "api_error", "timeout_error", "overloaded_error":
		return true
	default:
		return false
	}
}

type responseUsage struct {
	Model             string
	InputTokens       int64
	OutputTokens      int64
	CachedInputTokens int64
	FinishReason      string
	ToolCallCount     int64
}

// parseResponseUsage extracts authoritative non-streaming provider usage.
// Falling back to tokenization remains important for providers that omit it,
// but tokenizing the response envelope itself must never replace reported API
// usage or obscure the model that actually served a routed request.
func parseResponseUsage(body []byte) (responseUsage, bool) {
	var response struct {
		Model  string `json:"model"`
		Status string `json:"status"`
		Usage  struct {
			PromptTokens     *int64 `json:"prompt_tokens"`
			CompletionTokens *int64 `json:"completion_tokens"`
			InputTokens      *int64 `json:"input_tokens"`
			OutputTokens     *int64 `json:"output_tokens"`
			InputDetails     struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
		Output []struct {
			Type string `json:"type"`
		} `json:"output"`
	}
	if json.Unmarshal(body, &response) == nil {
		input, output := response.Usage.PromptTokens, response.Usage.CompletionTokens
		if input == nil || output == nil {
			input, output = response.Usage.InputTokens, response.Usage.OutputTokens
		}
		if input != nil && output != nil && *input >= 0 && *output >= 0 {
			return responseUsage{
				Model: response.Model, InputTokens: *input, OutputTokens: *output,
				CachedInputTokens: response.Usage.InputDetails.CachedTokens,
				FinishReason:      response.Status, ToolCallCount: countToolItems(response.Output),
			}, true
		}
	}
	return parseSSEUsage(body)
}

func parseSSEUsage(body []byte) (responseUsage, bool) {
	normalized := bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
	var aggregate responseUsage
	var inputSeen, outputSeen bool
	var streamedToolCalls int64
	for _, frame := range bytes.Split(normalized, []byte("\n\n")) {
		var data []byte
		for _, line := range bytes.Split(frame, []byte("\n")) {
			if bytes.HasPrefix(line, []byte("data:")) {
				part := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
				if len(part) > 0 && !bytes.Equal(part, []byte("[DONE]")) {
					data = append(data, part...)
				}
			}
		}
		if len(data) == 0 {
			continue
		}
		var event struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
			Message  struct {
				Model string `json:"model"`
				Usage struct {
					InputTokens          int64 `json:"input_tokens"`
					CacheReadInputTokens int64 `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				OutputTokens *int64 `json:"output_tokens"`
			} `json:"usage"`
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Item struct {
				Type string `json:"type"`
			} `json:"item"`
			ContentBlock struct {
				Type string `json:"type"`
			} `json:"content_block"`
		}
		if json.Unmarshal(data, &event) != nil {
			continue
		}
		switch event.Type {
		case "response.completed", "response.done":
			if usage, ok := parseResponseUsage(event.Response); ok {
				aggregate = usage
				inputSeen, outputSeen = true, true
			}
		case "response.output_item.done":
			if isToolItem(event.Item.Type) {
				streamedToolCalls++
			}
		case "message_start":
			aggregate.Model = event.Message.Model
			aggregate.InputTokens = event.Message.Usage.InputTokens
			aggregate.CachedInputTokens = event.Message.Usage.CacheReadInputTokens
			inputSeen = true
		case "message_delta":
			if event.Usage.OutputTokens != nil && *event.Usage.OutputTokens >= 0 {
				aggregate.OutputTokens = *event.Usage.OutputTokens
				outputSeen = true
			}
			if event.Delta.StopReason != "" {
				aggregate.FinishReason = event.Delta.StopReason
			}
		case "content_block_start":
			if isToolItem(event.ContentBlock.Type) {
				streamedToolCalls++
			}
		}
	}
	if aggregate.ToolCallCount == 0 {
		aggregate.ToolCallCount = streamedToolCalls
	}
	if !inputSeen || !outputSeen || aggregate.InputTokens < 0 || aggregate.OutputTokens < 0 {
		return responseUsage{}, false
	}
	return aggregate, true
}

func countToolItems(items []struct {
	Type string `json:"type"`
}) int64 {
	var count int64
	for _, item := range items {
		if isToolItem(item.Type) {
			count++
		}
	}
	return count
}

func isToolItem(kind string) bool {
	return kind == "tool_use" || kind == "server_tool_use" || strings.HasSuffix(kind, "_call")
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

		executionID := strings.TrimSpace(r.Header.Get(headerExecutionID))
		if len(executionID) > 256 {
			executionID = ""
		}
		obs := &requestObservation{
			Start:      time.Now().UTC(),
			Provider:   provider.ID,
			Prefix:     provider.Prefix,
			CostSource: s.costSourceFor(provider.ID),

			WorkflowID:  r.Header.Get(headerWorkflowID),
			AgentID:     r.Header.Get(headerAgentID),
			SessionID:   r.Header.Get(headerSessionID),
			UserID:      r.Header.Get(headerUserID),
			ExecutionID: executionID,
		}
		// Execution attribution is TokenOps-local metadata, not provider
		// input. Capture it before stripping so the identifier is not sent
		// to the upstream API.
		r.Header.Del(headerExecutionID)
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
			if provider.ID == eventschema.ProviderAnthropic {
				obs.ManualThinking, obs.SamplingParameter = anthropicCompatibilityTraits(body)
			}
			if s.tokenizer != nil {
				if n, err := s.tokenizer.PreflightCount(provider.ID, body); err == nil {
					obs.InputTokens = int64(n)
					obs.ContextSize = int64(n)
					obs.InputCounted = true
				}
			}
		} else {
			// No body to count is not a failure to count: an empty
			// request really did carry zero input tokens.
			obs.InputCounted = true
		}

		ctx := context.WithValue(r.Context(), observationKey{}, obs)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func anthropicCompatibilityTraits(body []byte) (manualThinking, samplingParameter bool) {
	var request struct {
		Thinking struct {
			Type         string          `json:"type"`
			BudgetTokens json.RawMessage `json:"budget_tokens"`
		} `json:"thinking"`
		Temperature json.RawMessage `json:"temperature"`
		TopP        json.RawMessage `json:"top_p"`
		TopK        json.RawMessage `json:"top_k"`
	}
	if json.Unmarshal(body, &request) != nil {
		return false, false
	}
	manualThinking = request.Thinking.Type == "enabled" && len(request.Thinking.BudgetTokens) > 0 && string(request.Thinking.BudgetTokens) != "null"
	samplingParameter = rawJSONPresent(request.Temperature) || rawJSONPresent(request.TopP) || rawJSONPresent(request.TopK)
	return manualThinking, samplingParameter
}

func rawJSONPresent(value json.RawMessage) bool {
	return len(value) > 0 && string(value) != "null"
}

// associationFor ties an emitted event to the work ontology.
//
// The proxy learns who is working from the session header and nothing
// else: it sees a request, not a goal. `session:<id>` is exactly the
// actor form the task-ledger adapter produces, so events and
// reconstructed work name the same actor without a translation table
// between them.
//
// Work and Execution are deliberately left empty. Inventing them here
// would attribute every request to a goal nobody stated, which is the
// class of confident fiction the ontology exists to avoid — an
// unassociated event is an honest gap, and gaps are fillable.
func associationFor(obs *requestObservation) eventschema.Association {
	if obs == nil {
		return eventschema.Association{}
	}
	association := eventschema.Association{Execution: obs.ExecutionID}
	if obs.SessionID != "" {
		association.Actor = "session:" + obs.SessionID
	}
	return association
}
