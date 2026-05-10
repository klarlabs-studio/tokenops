package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// seedReplayDB writes a small set of PromptEvents into a fresh sqlite
// store and returns its path. Used by the cli replay tests so we exercise
// the end-to-end command without requiring the daemon.
func seedReplayDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "events.db")

	ctx := context.Background()
	store, err := sqlite.Open(ctx, path, sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Add(-2 * time.Hour)
	long := strings.Repeat("the quick brown fox jumps over the lazy dog ", 80)
	longer := strings.Repeat("alpha beta gamma delta epsilon zeta eta theta iota kappa ", 80)

	bodies := [][]byte{
		[]byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"` + long + `"},{"role":"assistant","content":"` + long + `"}]}`),
		[]byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"` + longer + `"},{"role":"user","content":"` + longer + `"}]}`),
	}

	for i, body := range bodies {
		env := &eventschema.Envelope{
			ID:            uuid.NewString(),
			SchemaVersion: eventschema.SchemaVersion,
			Type:          eventschema.EventTypePrompt,
			Timestamp:     now.Add(time.Duration(i) * time.Minute),
			Source:        "test",
			Payload: &eventschema.PromptEvent{
				PromptHash:    "sha256:abc",
				Provider:      eventschema.ProviderOpenAI,
				RequestModel:  "gpt-4o-mini",
				ResponseModel: "gpt-4o-mini",
				InputTokens:   int64(len(body) / 4),
				OutputTokens:  64,
				TotalTokens:   int64(len(body)/4) + 64,
				ContextSize:   int64(len(body) / 4),
				Latency:       250 * time.Millisecond,
				Streaming:     false,
				Status:        200,
				WorkflowID:    "wf-test",
				SessionID:     "sess-test",
				AgentID:       "agent-test",
			},
		}
		_ = body
		if err := store.Append(ctx, env); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	return path
}

func TestReplayTextRendersSavings(t *testing.T) {
	path := seedReplayDB(t)
	out, err := executeRoot(t,
		"replay", "sess-test",
		"--db", path,
		"--workflow", "wf-test",
	)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	for _, want := range []string{
		"Replay results",
		"prompts replayed:  2",
		"original input:",
		"estimated savings:",
		"STEP",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestReplayJSONShape(t *testing.T) {
	path := seedReplayDB(t)
	out, err := executeRoot(t,
		"replay", "sess-test",
		"--db", path,
		"--json",
	)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("not json: %v\n%s", err, out)
	}
	if _, ok := parsed["result"]; !ok {
		t.Errorf("missing result key: %v", parsed)
	}
	if _, ok := parsed["selector"]; !ok {
		t.Errorf("missing selector key: %v", parsed)
	}
}

func TestReplayMissingSelectorErrors(t *testing.T) {
	path := seedReplayDB(t)
	_, err := executeRoot(t, "replay", "--db", path)
	if err == nil {
		t.Fatal("expected error when no selector given")
	}
	if !strings.Contains(err.Error(), "SESSION_ID") {
		t.Errorf("error = %q", err)
	}
}

func TestReplayEmptyMatchErrors(t *testing.T) {
	path := seedReplayDB(t)
	_, err := executeRoot(t,
		"replay", "missing-session",
		"--db", path,
	)
	if err == nil {
		t.Fatal("expected error for empty session match")
	}
	if !strings.Contains(err.Error(), "no prompt events") {
		t.Errorf("error = %q", err)
	}
}

func TestParseSinceAcceptsDuration(t *testing.T) {
	t1, err := parseSince("2h")
	if err != nil {
		t.Fatalf("2h: %v", err)
	}
	if time.Since(t1) < 90*time.Minute || time.Since(t1) > 150*time.Minute {
		t.Errorf("2h parsed wrong: %s", time.Since(t1))
	}
	t2, err := parseSince("1d")
	if err != nil {
		t.Fatalf("1d: %v", err)
	}
	if time.Since(t2) < 23*time.Hour || time.Since(t2) > 25*time.Hour {
		t.Errorf("1d parsed wrong: %s", time.Since(t2))
	}
	if _, err := parseSince("zonk"); err == nil {
		t.Errorf("expected error for bogus value")
	}
}
