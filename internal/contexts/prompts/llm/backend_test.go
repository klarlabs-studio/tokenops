package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOllamaBackendGenerate(t *testing.T) {
	var lastBody atomic.Pointer[[]byte]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("path = %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		lastBody.Store(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"response":"hello world","done":true}`)
	}))
	t.Cleanup(srv.Close)

	be, err := New(Config{Kind: "ollama", Endpoint: srv.URL, Model: "llama3"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := be.Generate(context.Background(), "be helpful", "hi")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if out != "hello world" {
		t.Errorf("output = %q", out)
	}
	if be.Name() != "ollama:llama3" {
		t.Errorf("name = %q", be.Name())
	}
	body := *lastBody.Load()
	if !strings.Contains(string(body), `"system":"be helpful"`) {
		t.Errorf("system prompt missing: %s", body)
	}
}

func TestOllamaBackendErrorPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = io.WriteString(w, `{"error":"server boom"}`)
	}))
	t.Cleanup(srv.Close)
	be, _ := New(Config{Kind: "ollama", Endpoint: srv.URL, Model: "x"})
	_, err := be.Generate(context.Background(), "", "ping")
	if err == nil || !strings.Contains(err.Error(), "ollama http 500") {
		t.Errorf("expected http 500 error, got %v", err)
	}
}

func TestOpenAICompatBackendGenerate(t *testing.T) {
	var bearer atomic.Pointer[string]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		v := r.Header.Get("Authorization")
		bearer.Store(&v)
		var got map[string]any
		_ = json.NewDecoder(r.Body).Decode(&got)
		if got["model"] != "phi3" {
			t.Errorf("model = %v", got["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"hi back"}}]}`)
	}))
	t.Cleanup(srv.Close)

	be, err := New(Config{Kind: "openai_compat", Endpoint: srv.URL, Model: "phi3", APIKey: "secret-x"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := be.Generate(context.Background(), "system", "hello")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if out != "hi back" {
		t.Errorf("output = %q", out)
	}
	if got := bearer.Load(); got == nil || *got != "Bearer secret-x" {
		t.Errorf("bearer header missing or wrong: %v", got)
	}
}

func TestOpenAICompatErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"error":{"message":"upstream squelched"}}`)
	}))
	t.Cleanup(srv.Close)
	be, _ := New(Config{Kind: "openai_compat", Endpoint: srv.URL, Model: "x"})
	_, err := be.Generate(context.Background(), "", "ping")
	if err == nil || !strings.Contains(err.Error(), "upstream squelched") {
		t.Errorf("expected upstream error surfaced, got %v", err)
	}
}

func TestNewRejectsMissingFields(t *testing.T) {
	if _, err := New(Config{Model: "x", Kind: "openai_compat"}); err == nil {
		t.Error("openai_compat without endpoint should error")
	}
	if _, err := New(Config{Kind: "ollama"}); err == nil {
		t.Error("missing model should error")
	}
	if _, err := New(Config{Kind: "bogus", Model: "x"}); err == nil {
		t.Error("unknown kind should error")
	}
}

func TestNewDefaultsKindAndEndpoint(t *testing.T) {
	be, err := New(Config{Model: "llama3"})
	if err != nil {
		t.Fatalf("New default: %v", err)
	}
	if !strings.HasPrefix(be.Name(), "ollama:") {
		t.Errorf("default backend should be ollama, got %q", be.Name())
	}
}
