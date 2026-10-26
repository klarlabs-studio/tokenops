package proxy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/prefixlint"
)

// GET /api/coach/lint-context answers the volatile-prefix lint: the
// user's files and hooks, the project named by ?root=, and the hook
// output the daemon's transcripts recorded. Hook output itself is never
// served, only the matched token.
func TestLintContextRoute(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".claude", "settings.json"),
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"~/bin/brief"}]}]}}`)
	project := filepath.Join(home, "repo")
	write(filepath.Join(project, "CLAUDE.md"), "Keep go 1.26.\n")
	root := filepath.Join(home, "transcripts")
	write(filepath.Join(root, "p", "s1.jsonl"),
		`{"type":"attachment","sessionId":"s1","attachment":{"type":"hook_success","hookName":"SessionStart:startup","hookEvent":"SessionStart","command":"~/bin/brief","stdout":"private brief, built 2026-10-26T08:00:00Z"}}`+"\n")
	s := New("127.0.0.1:0", WithSessions(func() SessionRoots { return SessionRoots{Prompts: root} }))

	var r prefixlint.Report
	if code := getAPI(t, s, "/api/coach/lint-context?root="+project, &r); code != http.StatusOK {
		t.Fatalf("lint-context = %d", code)
	}
	if r.Transcripts != 1 || r.High != 1 || len(r.Findings) != 1 || r.Findings[0].Token != "2026-10-26T08:00:00Z" {
		t.Errorf("report = %+v", r)
	}
	rec := httptest.NewRecorder()
	s.apiMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/coach/lint-context", nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "private brief") {
		t.Errorf("without root = %d %s", rec.Code, rec.Body)
	}
	bad := httptest.NewRecorder()
	s.apiMux().ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/api/coach/lint-context?root="+filepath.Join(home, "absent"), nil))
	if bad.Code != http.StatusBadRequest {
		t.Errorf("absent root = %d, want 400", bad.Code)
	}
}
