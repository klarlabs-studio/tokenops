package opencode

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// newTestDB creates a minimal opencode-shaped database with the given message
// rows and returns its path.
func newTestDB(t *testing.T, rows map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER, time_updated INTEGER, data TEXT NOT NULL)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for id, data := range rows {
		if _, err := db.Exec(`INSERT INTO message (id, session_id, data) VALUES (?, ?, ?)`, id, "sess-1", data); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	return path
}

func TestReadMessagesParsesAssistantTurn(t *testing.T) {
	assistant := `{"role":"assistant","time":{"created":1771056613283},"modelID":"claude-opus-4.6","providerID":"github-copilot","path":{"cwd":"/Users/x/Developer/aios","root":"/Users/x/Developer/aios"},"cost":0.12,"tokens":{"input":63976,"output":1013,"reasoning":50,"cache":{"read":100,"write":200}}}`
	user := `{"role":"user","time":{"created":1771056600000},"tokens":{"input":0,"output":0}}`
	path := newTestDB(t, map[string]string{"m1": assistant, "m2": user})

	var got []Turn
	if err := ReadMessages(path, func(tn Turn) error { got = append(got, tn); return nil }); err != nil {
		t.Fatalf("ReadMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 assistant turn (user turn skipped), got %d", len(got))
	}
	tn := got[0]
	if tn.Provider != eventschema.ProviderGitHub {
		t.Errorf("provider = %q, want github", tn.Provider)
	}
	if tn.Project != "aios" {
		t.Errorf("project = %q, want aios", tn.Project)
	}
	// input(63976) + cache.read(100) + cache.write(200) = 64276
	if tn.InputTokens != 64276 {
		t.Errorf("input = %d, want 64276", tn.InputTokens)
	}
	if tn.CachedTokens != 100 {
		t.Errorf("cached = %d, want 100", tn.CachedTokens)
	}
	// output(1013) + reasoning(50) = 1063
	if tn.OutputTokens != 1063 {
		t.Errorf("output = %d, want 1063", tn.OutputTokens)
	}
	if tn.Model != "claude-opus-4.6" {
		t.Errorf("model = %q", tn.Model)
	}
}

func TestReadMessagesMissingDBIsNotError(t *testing.T) {
	if err := ReadMessages(filepath.Join(t.TempDir(), "nope.db"), func(Turn) error {
		t.Fatal("visit should not be called for a missing db")
		return nil
	}); err != nil {
		t.Errorf("missing db should be a no-op, got %v", err)
	}
}

func TestNewEnvelopeAttribution(t *testing.T) {
	env := newEnvelope(Turn{
		ID: "m1", SessionID: "s1", Project: "aios", Provider: eventschema.ProviderGitHub,
		InputTokens: 100, OutputTokens: 20,
	}, "")
	pe, ok := env.Payload.(*eventschema.PromptEvent)
	if !ok {
		t.Fatalf("payload is not a PromptEvent: %T", env.Payload)
	}
	if pe.AgentID != "opencode:aios" {
		t.Errorf("agent = %q", pe.AgentID)
	}
	if pe.WorkflowID != "opencode:aios:s1" {
		t.Errorf("workflow = %q", pe.WorkflowID)
	}
	if pe.TotalTokens != 120 {
		t.Errorf("total = %d, want 120", pe.TotalTokens)
	}
	if env.Source != SourceTag {
		t.Errorf("source = %q", env.Source)
	}
}
