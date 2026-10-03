package geminicli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, root, project, name, body string, mtime time.Time) string {
	t.Helper()
	dir := filepath.Join(root, project, "chats")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}

func collect(t *testing.T, path string, now time.Time) []Turn {
	t.Helper()
	var out []Turn
	if err := ReadFile(path, now, func(tr Turn) error { out = append(out, tr); return nil }); err != nil {
		t.Fatal(err)
	}
	return out
}

// The older format: one document with a messages array. The user's prompt
// and the model's text are never part of a Turn.
func TestReadsTheDocumentFormat(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	hash := strings.Repeat("ab", 32)
	path := write(t, root, hash, "session-2026-01-19T08-41-a15aa484.json", `{"sessionId":"s1","projectHash":"`+hash+`","messages":[
	 {"id":"u1","timestamp":"2026-01-19T08:41:00Z","type":"user","content":"secret instruction"},
	 {"id":"g1","timestamp":"2026-01-19T08:41:05Z","type":"gemini","content":"answer","model":"gemini-3-pro-preview",
	  "tokens":{"input":1000,"output":50,"cached":400,"thoughts":30,"tool":20,"total":1100}}]}`, now.Add(-time.Hour))
	got := collect(t, path, now)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	tr := got[0]
	if tr.SessionID != "s1" || tr.Project != "gemini-abababab" || tr.Model != "gemini-3-pro-preview" ||
		tr.InputTokens != 1020 || tr.CachedTokens != 400 || tr.OutputTokens != 80 || tr.Timestamp.IsZero() {
		t.Errorf("turn %+v", tr)
	}
}

// The newer stream appends a message again as it updates: the last copy
// counts, control lines are skipped, and a turn still streaming waits.
func TestReadsTheStreamOnceEachWithItsFinalFigures(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	body := strings.Join([]string{
		`{"sessionId":"s2","projectHash":"x","startTime":"2026-10-03T10:00:00Z"}`,
		`{"id":"g1","timestamp":"2026-10-03T10:00:01Z","type":"gemini","model":"gemini-3-flash","tokens":{"input":10,"output":1}}`,
		`{"$set":{"lastUpdated":"2026-10-03T10:00:02Z"}}`,
		`{"id":"g1","timestamp":"2026-10-03T10:00:01Z","type":"gemini","model":"gemini-3-flash","tokens":{"input":100,"output":40}}`,
		`{"id":"u2","timestamp":"2026-10-03T10:01:00Z","type":"user"}`,
		`{"id":"g2","timestamp":"2026-10-03T10:01:01Z","type":"gemini","model":"gemini-3-flash","tokens":{"input":200,"output":5}}`,
	}, "\n") + "\n"
	path := write(t, root, "tokenops", "session-2026-10-03T10-00-abcd1234.jsonl", body, now)

	streaming := collect(t, path, now)
	if len(streaming) != 1 || streaming[0].ID != "g1" || streaming[0].InputTokens != 100 || streaming[0].OutputTokens != 40 {
		t.Fatalf("while the session is live: %+v", streaming)
	}
	if streaming[0].Project != "tokenops" {
		t.Errorf("project %q", streaming[0].Project)
	}
	if settledTurns := collect(t, path, now.Add(2*time.Minute)); len(settledTurns) != 2 {
		t.Errorf("once quiet: %+v", settledTurns)
	}
}

// The poller emits each turn once, however often it scans.
func TestPollerEmitsEachTurnOnce(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	write(t, root, "p", "session-1.jsonl", `{"sessionId":"s"}`+"\n"+
		`{"id":"g1","timestamp":"2026-10-03T10:00:01Z","type":"gemini","model":"m","tokens":{"input":10,"output":1}}`+"\n", now.Add(-time.Hour))
	bus := &countingBus{}
	p := NewPoller(bus, PollerOptions{Now: func() time.Time { return now }})
	p.Scan(context.Background(), root)
	p.Scan(context.Background(), root)
	if bus.n != 1 {
		t.Fatalf("published %d", bus.n)
	}
}
