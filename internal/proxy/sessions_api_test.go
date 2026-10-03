package proxy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/sessions"
)

const typedInstruction = "rewrite the boundary heuristic"

func sessionsServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","timestamp":"2099-01-01T10:00:00Z","sessionId":"s","message":{"content":"` + typedInstruction + `"}}`,
		`{"type":"assistant","timestamp":"2099-01-01T10:00:01Z","sessionId":"s","message":{"usage":{"input_tokens":100},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/a.go"}}]}}`,
		`{"type":"assistant","timestamp":"2099-01-01T10:00:02Z","sessionId":"s","message":{"usage":{"input_tokens":150},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/b.go"}}]}}`,
	}
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	return New("127.0.0.1:0", WithSessions(func() SessionRoots { return SessionRoots{Transcripts: root, Prompts: root} }))
}

// The API serves the shape of the work, never the operator's words
// (ADR 0010 §5).
func TestStoryRouteWithholdsTheInstruction(t *testing.T) {
	s := sessionsServer(t)
	rec := httptest.NewRecorder()
	s.apiMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/story?all=true", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("story = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), typedInstruction) {
		t.Fatalf("the API served the typed instruction: %s", rec.Body)
	}
	var st sessions.Story
	if code := getAPI(t, s, "/api/story?all=true", &st); code != http.StatusOK || len(st.Tasks) != 1 || !st.TitlesWithheld || st.Tasks[0].ToolCalls != 2 {
		t.Fatalf("story = %+v", st)
	}
}

func TestDXAndPromptRoutesAnswer(t *testing.T) {
	s := sessionsServer(t)
	var dx sessions.DX
	if code := getAPI(t, s, "/api/dx?all=true", &dx); code != http.StatusOK || dx.Metrics.Prompts != 1 || dx.Window != "all history" {
		t.Errorf("dx = %d %+v", code, dx)
	}
	rec := httptest.NewRecorder()
	s.apiMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/coach/prompts?since=2098-01-01T00:00:00Z", nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), typedInstruction) {
		t.Errorf("prompts = %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{"/api/dx?days=week", "/api/story?all=maybe", "/api/coach/prompts?limit=x"} {
		r := httptest.NewRecorder()
		s.apiMux().ServeHTTP(r, httptest.NewRequest(http.MethodGet, bad, nil))
		if r.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", bad, r.Code)
		}
	}
}
