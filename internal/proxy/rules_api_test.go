package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func writeRulesAPICorpus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"CLAUDE.md":            "# Testing\nuse tdd\n## Style\nbe concise\n## Other\nexplain thoroughly with detailed reasoning\n",
		"AGENTS.md":            "# Testing\nuse tdd\n",
		".cursor/rules/go.mdc": "# Go\nprefer composition\n",
	}
	for rel, body := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return dir
}

func TestRulesCacheInvalidatesOnCanonicalCorpusReload(t *testing.T) {
	h, err := NewRulesHandlers(t.TempDir(), "repo")
	if err != nil {
		t.Fatal(err)
	}
	h.storeAnalyze("key", []byte("cached"))
	bus := events.NewAsync(events.NoopSink{}, events.Options{})
	defer func() { _ = bus.Close(time.Second) }()
	detach := h.AttachEventBus(bus)
	defer detach()

	bus.Publish(&eventschema.Envelope{
		ID: "other-kind", SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypeDomain, Timestamp: time.Now().UTC(),
		Payload: &eventschema.DomainEvent{Kind: "workflow.started"},
	})
	if _, ok := h.cachedAnalyze("key"); !ok {
		t.Fatal("unrelated domain event invalidated the rules cache")
	}
	bus.Publish(&eventschema.Envelope{
		ID: "reload", SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypeDomain, Timestamp: time.Now().UTC(),
		Payload: &eventschema.DomainEvent{Kind: "rule_corpus.reloaded"},
	})
	if _, ok := h.cachedAnalyze("key"); ok {
		t.Fatal("canonical rule_corpus.reloaded event did not invalidate cache")
	}
}

func newRulesMux(t *testing.T, root string) *http.ServeMux {
	t.Helper()
	h, err := NewRulesHandlers(root, "repo")
	if err != nil {
		t.Fatalf("NewRulesHandlers: %v", err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func TestRulesAnalyzeAPI(t *testing.T) {
	root := writeRulesAPICorpus(t)
	mux := newRulesMux(t, root)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/rules/analyze")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	docs, _ := body["documents"].([]any)
	if len(docs) == 0 {
		t.Errorf("expected documents in response, got %+v", body)
	}
}

func TestRulesConflictsAPI(t *testing.T) {
	root := writeRulesAPICorpus(t)
	mux := newRulesMux(t, root)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/rules/conflicts")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["findings"]; !ok {
		t.Errorf("findings key missing: %+v", body)
	}
}

func TestRulesCompressAPI(t *testing.T) {
	root := writeRulesAPICorpus(t)
	mux := newRulesMux(t, root)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/rules/compress")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "original_tokens") {
		t.Errorf("compress response missing original_tokens: %s", raw)
	}
}

func TestRulesInjectAPI(t *testing.T) {
	root := writeRulesAPICorpus(t)
	mux := newRulesMux(t, root)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/rules/inject?keywords=testing")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "Selections") {
		t.Errorf("inject response missing Selections: %s", raw)
	}
}
