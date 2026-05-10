// Package llm hosts the pluggable local-model backend used by coaching
// to generate natural-language summaries (and, eventually, scorer
// outputs for prompt compression). Two backends ship by default:
//
//   - OllamaBackend speaks Ollama's native HTTP API
//     (http://localhost:11434/api/generate). Ideal for users running
//     local models via Ollama or any binary that re-exports its API.
//   - OpenAICompatBackend speaks the OpenAI chat-completions surface
//     (http://localhost:8000/v1/chat/completions or similar). Compatible
//     with vLLM, llama.cpp's server, Together's local proxy, LM Studio,
//     etc.
//
// The Backend interface is small: Generate takes a system prompt + user
// prompt and returns the model's completion. Streaming is intentionally
// excluded — coaching summaries are short and offline.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Backend is the pluggable local-model surface.
type Backend interface {
	// Generate returns the model's response to (system, user).
	Generate(ctx context.Context, system, user string) (string, error)
	// Name reports a human-readable backend identifier (used in logs).
	Name() string
}

// Config selects + tunes a backend.
type Config struct {
	// Kind is "ollama" (default) or "openai_compat".
	Kind string
	// Endpoint is the base URL of the local server (no trailing slash).
	// Defaults: ollama → http://127.0.0.1:11434; openai_compat → none
	// (must be supplied).
	Endpoint string
	// Model is the model name as the backend knows it. Required.
	Model string
	// APIKey is forwarded as a bearer token (openai_compat). Most local
	// servers accept any non-empty value; some (vLLM with --api-key)
	// require a match.
	APIKey string
	// Headers are added on every request (e.g. tenant header).
	Headers map[string]string
	// Timeout caps each request. Default 60s.
	Timeout time.Duration
	// HTTPClient overrides the default http.Client (for tests).
	HTTPClient *http.Client
}

// New constructs a Backend per cfg. Returns an error when required
// fields are missing or the kind is unknown.
func New(cfg Config) (Backend, error) {
	if cfg.Model == "" {
		return nil, errors.New("llm: Model must not be empty")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: cfg.Timeout}
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Kind)) {
	case "", "ollama":
		ep := cfg.Endpoint
		if ep == "" {
			ep = "http://127.0.0.1:11434"
		}
		return &OllamaBackend{
			endpoint: strings.TrimRight(ep, "/"),
			model:    cfg.Model,
			client:   cfg.HTTPClient,
			headers:  cfg.Headers,
		}, nil
	case "openai_compat", "openai":
		if cfg.Endpoint == "" {
			return nil, errors.New("llm: openai_compat requires Endpoint")
		}
		return &OpenAICompatBackend{
			endpoint: strings.TrimRight(cfg.Endpoint, "/"),
			model:    cfg.Model,
			apiKey:   cfg.APIKey,
			client:   cfg.HTTPClient,
			headers:  cfg.Headers,
		}, nil
	default:
		return nil, fmt.Errorf("llm: unknown kind %q", cfg.Kind)
	}
}

// --- Ollama backend ---------------------------------------------------

// OllamaBackend talks to Ollama's /api/generate. We use generate (not
// chat) because the simple system+prompt envelope is sufficient and
// avoids the message-array shape variation across Ollama releases.
type OllamaBackend struct {
	endpoint string
	model    string
	client   *http.Client
	headers  map[string]string
}

// Name implements Backend.
func (b *OllamaBackend) Name() string { return "ollama:" + b.model }

type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	System string `json:"system,omitempty"`
	Stream bool   `json:"stream"`
}

type ollamaResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
	Error    string `json:"error,omitempty"`
}

// Generate implements Backend.
func (b *OllamaBackend) Generate(ctx context.Context, system, user string) (string, error) {
	payload := ollamaRequest{
		Model:  b.model,
		Prompt: user,
		System: system,
		Stream: false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("ollama marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range b.headers {
		req.Header.Set(k, v)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return "", fmt.Errorf("ollama read: %w", err)
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("ollama http %d: %s", resp.StatusCode, truncateError(raw))
	}
	var parsed ollamaResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("ollama decode: %w (body=%s)", err, truncateError(raw))
	}
	if parsed.Error != "" {
		return "", fmt.Errorf("ollama error: %s", parsed.Error)
	}
	return parsed.Response, nil
}

// --- OpenAI-compatible backend ---------------------------------------

// OpenAICompatBackend POSTs to /v1/chat/completions on a configurable
// endpoint. It expects the same JSON shape as api.openai.com so vLLM,
// llama.cpp's server, LM Studio, and similar drop-in shims work
// without further configuration.
type OpenAICompatBackend struct {
	endpoint string
	model    string
	apiKey   string
	client   *http.Client
	headers  map[string]string
}

// Name implements Backend.
func (b *OpenAICompatBackend) Name() string { return "openai_compat:" + b.model }

type openAIChatRequest struct {
	Model    string              `json:"model"`
	Messages []openAIChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Generate implements Backend.
func (b *OpenAICompatBackend) Generate(ctx context.Context, system, user string) (string, error) {
	msgs := make([]openAIChatMessage, 0, 2)
	if system != "" {
		msgs = append(msgs, openAIChatMessage{Role: "system", Content: system})
	}
	msgs = append(msgs, openAIChatMessage{Role: "user", Content: user})

	payload := openAIChatRequest{
		Model:    b.model,
		Messages: msgs,
		Stream:   false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("openai_compat marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.apiKey)
	}
	for k, v := range b.headers {
		req.Header.Set(k, v)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai_compat request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return "", fmt.Errorf("openai_compat read: %w", err)
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("openai_compat http %d: %s", resp.StatusCode, truncateError(raw))
	}
	var parsed openAIChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("openai_compat decode: %w (body=%s)", err, truncateError(raw))
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("openai_compat error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("openai_compat empty choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

// truncateError trims a body to ≤200 bytes so error messages do not
// blow up logs with multi-MB tokenizer dumps.
func truncateError(b []byte) string {
	const max = 200
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}
