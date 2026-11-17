package copilotcli

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// writeSession writes one session's events under root, in the layout the
// current Copilot CLI uses (session-state/<id>/events.jsonl) or, with
// flat, the layout of early versions (session-state/<id>.jsonl).
func writeSession(t *testing.T, root, id string, flat bool, lines ...string) string {
	t.Helper()
	path := filepath.Join(root, id, "events.jsonl")
	if flat {
		path = filepath.Join(root, id+".jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func collect(t *testing.T, path string) []Usage {
	t.Helper()
	var out []Usage
	if err := ReadFile(path, func(u Usage) error { out = append(out, u); return nil }); err != nil {
		t.Fatal(err)
	}
	return out
}

const (
	startLine = `{"type":"session.start","id":"e1","timestamp":"2026-11-10T09:00:00Z","parentId":null,` +
		`"data":{"sessionId":"s1","version":1,"producer":"copilot-agent","copilotVersion":"1.0.80","startTime":"2026-11-10T09:00:00Z",` +
		`"selectedModel":"claude-sonnet-4.5","context":{"cwd":"/work/shop/api","gitRoot":"/work/shop","branch":"main"}}}`
	userLine      = `{"type":"user.message","id":"e2","timestamp":"2026-11-10T09:00:05Z","parentId":"e1","data":{"content":"a private instruction"}}`
	assistantLine = `{"type":"assistant.message","id":"e3","timestamp":"2026-11-10T09:00:09Z","parentId":"e2","data":{"messageId":"m1","content":"a private reply"}}`
)

// shutdownLine writes a shutdown record on one line, as the CLI does.
func shutdownLine(id, at, metrics string) string {
	metrics = strings.Join(strings.Fields(metrics), "")
	return `{"type":"session.shutdown","id":"` + id + `","timestamp":"` + at + `","parentId":"e3","data":{"shutdownType":"routine",` +
		`"sessionStartTime":1794301200000,"totalApiDurationMs":1000,"codeChanges":{"linesAdded":0,"linesRemoved":0,"filesModified":[]},` +
		`"modelMetrics":` + metrics + `}}`
}

// A session's tokens are in its shutdown record, per model; the prompt and
// the reply are never part of a Usage.
func TestReadsEachModelsTokensFromTheShutdown(t *testing.T) {
	root := t.TempDir()
	path := writeSession(t, root, "s1", false, startLine, userLine, assistantLine,
		shutdownLine("e9", "2026-11-10T09:30:00Z", `{
		 "claude-sonnet-4.5":{"requests":{"count":12,"cost":12},"usage":{"inputTokens":50000,"outputTokens":3000,"cacheReadTokens":40000,"cacheWriteTokens":2000,"reasoningTokens":100}},
		 "gpt-5-mini":{"requests":{"count":2,"cost":0},"usage":{"inputTokens":800,"outputTokens":90,"cacheReadTokens":0,"cacheWriteTokens":0}}}`))
	got := collect(t, path)
	sort.Slice(got, func(i, j int) bool { return got[i].Model < got[j].Model })
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	want := Usage{
		ID: "e9|claude-sonnet-4.5", SessionID: "s1", Project: "shop", CWD: "/work/shop/api", Branch: "main",
		Model: "claude-sonnet-4.5", Requests: 12, Input: 50000, CacheRead: 40000, CacheWrite: 2000,
		Output: 3000, Reasoning: 100, Timestamp: got[0].Timestamp,
	}
	if !reflect.DeepEqual(got[0], want) || got[0].Timestamp.IsZero() {
		t.Errorf("sonnet\n got %+v\nwant %+v", got[0], want)
	}
	if got[1].Model != "gpt-5-mini" || got[1].Input != 800 || got[1].Output != 90 || got[1].Requests != 2 {
		t.Errorf("mini %+v", got[1])
	}
	for _, u := range got {
		if strings.Contains(strings.ToLower(u.Project+u.CWD+u.Model), "private") {
			t.Errorf("content leaked into %+v", u)
		}
	}
}

// A resumed session writes another shutdown whose figures are the whole
// session's (Copilot CLI restores its accounting from the session's
// checkpoints on resume), so only what grew since the last one is new.
func TestAResumedSessionYieldsOnlyWhatGrew(t *testing.T) {
	root := t.TempDir()
	path := writeSession(t, root, "s1", false, startLine,
		shutdownLine("e5", "2026-11-10T09:30:00Z", `{"m":{"requests":{"count":3},"usage":{"inputTokens":1000,"outputTokens":100,"cacheReadTokens":600,"cacheWriteTokens":0}}}`),
		`{"type":"session.resume","id":"e6","timestamp":"2026-11-10T10:00:00Z","parentId":"e5","data":{"eventCount":5,"resumeTime":"2026-11-10T10:00:00Z"}}`,
		shutdownLine("e8", "2026-11-10T10:30:00Z", `{"m":{"requests":{"count":5},"usage":{"inputTokens":1500,"outputTokens":180,"cacheReadTokens":900,"cacheWriteTokens":0}},
		 "n":{"requests":{"count":1},"usage":{"inputTokens":70,"outputTokens":7,"cacheReadTokens":0,"cacheWriteTokens":0}}}`))
	got := collect(t, path)
	sort.Slice(got, func(i, j int) bool { return got[i].ID < got[j].ID })
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got[0].ID != "e5|m" || got[0].Input != 1000 || got[0].Output != 100 {
		t.Errorf("first %+v", got[0])
	}
	if got[1].ID != "e8|m" || got[1].Input != 500 || got[1].Output != 80 || got[1].CacheRead != 300 || got[1].Requests != 2 {
		t.Errorf("growth %+v", got[1])
	}
	if got[2].ID != "e8|n" || got[2].Input != 70 {
		t.Errorf("new model %+v", got[2])
	}
}

// Figures that shrank are not a growth to subtract from: the count
// started again, and the new figures are taken as they are.
func TestFiguresThatShrankAreTakenAsTheyAre(t *testing.T) {
	root := t.TempDir()
	path := writeSession(t, root, "s1", false, startLine,
		shutdownLine("e5", "2026-11-10T09:30:00Z", `{"m":{"usage":{"inputTokens":1000,"outputTokens":100,"cacheReadTokens":0,"cacheWriteTokens":0}}}`),
		shutdownLine("e8", "2026-11-10T10:30:00Z", `{"m":{"usage":{"inputTokens":400,"outputTokens":40,"cacheReadTokens":0,"cacheWriteTokens":0}}}`))
	got := collect(t, path)
	if len(got) != 2 || got[1].Input != 400 || got[1].Output != 40 {
		t.Fatalf("got %+v", got)
	}
}

// A session still running has no shutdown yet: it has no tokens to give,
// and nothing is guessed for it. Nor does an unchanged second shutdown.
func TestNoShutdownNoTokens(t *testing.T) {
	root := t.TempDir()
	if got := collect(t, writeSession(t, root, "s1", false, startLine, userLine, assistantLine)); len(got) != 0 {
		t.Errorf("running session: %+v", got)
	}
	same := `{"m":{"usage":{"inputTokens":10,"outputTokens":1,"cacheReadTokens":0,"cacheWriteTokens":0}}}`
	got := collect(t, writeSession(t, root, "s2", false, startLine,
		shutdownLine("e5", "2026-11-10T09:30:00Z", same), shutdownLine("e8", "2026-11-10T10:30:00Z", same)))
	if len(got) != 1 {
		t.Errorf("unchanged second shutdown: %+v", got)
	}
}

// Early versions kept a session as one flat file and wrote no shutdown;
// the session id comes from the file when no start record names it, and a
// half-written last line is skipped.
func TestFlatFilesAndPartialLines(t *testing.T) {
	root := t.TempDir()
	path := writeSession(t, root, "b3ca0f10", true,
		`{"type":"session.start","id":"e1","timestamp":"2026-11-10T09:00:00Z","data":{"producer":"copilot-agent","copilotVersion":"0.0.350"}}`,
		shutdownLine("e5", "2026-11-10T09:30:00Z", `{"m":{"usage":{"inputTokens":10,"outputTokens":1,"cacheReadTokens":0,"cacheWriteTokens":0}}}`),
		`{"type":"session.shutdown","id":"e6","timest`)
	got := collect(t, path)
	if len(got) != 1 || got[0].SessionID != "b3ca0f10" || got[0].Project != "unknown" {
		t.Fatalf("got %+v", got)
	}
}

func TestFindSessionFilesBothLayouts(t *testing.T) {
	root := t.TempDir()
	a := writeSession(t, root, "a", false, startLine)
	b := writeSession(t, root, "b", true, startLine)
	// Neither a lock nor a session's other artefacts are a session.
	if err := os.MkdirAll(filepath.Join(root, ".session-operation-locks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".session-operation-locks", "x.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "plan.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := FindSessionFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if want := []string{a, b}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
	if got, err := FindSessionFiles(filepath.Join(root, "missing")); err != nil || got != nil {
		t.Errorf("missing root: %v %v", got, err)
	}
}

func TestDefaultRootHonoursCopilotHome(t *testing.T) {
	t.Setenv("COPILOT_HOME", "/elsewhere/copilot")
	if got, err := DefaultRoot(); err != nil || got != filepath.Join("/elsewhere/copilot", "session-state") {
		t.Errorf("got %q %v", got, err)
	}
	t.Setenv("COPILOT_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got, err := DefaultRoot(); err != nil || got != filepath.Join(home, ".copilot", "session-state") {
		t.Errorf("got %q %v", got, err)
	}
}

// A line too long to read is skipped; the lines after it are still read,
// and a last line without a newline is read too.
func TestEachLineSkipsAnOverlongLine(t *testing.T) {
	in := `{"a":1}` + "\n" + strings.Repeat("x", 200_000) + "\n\n" + `{"b":2}` + "\r\n" + `{"c":3}`
	var got []string
	if err := eachLine(strings.NewReader(in), 100_000, func(b []byte) error { got = append(got, string(b)); return nil }); err != nil {
		t.Fatal(err)
	}
	if want := []string{`{"a":1}`, `{"b":2}`, `{"c":3}`}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
}

func TestSessionHead(t *testing.T) {
	root := t.TempDir()
	id, where, ok := SessionHead(writeSession(t, root, "dir-id", false, startLine, userLine))
	if !ok || id != "s1" || where.CWD != "/work/shop/api" || where.Branch != "main" || where.Project() != "shop" {
		t.Errorf("got %q %+v %v", id, where, ok)
	}
	if _, _, ok := SessionHead(writeSession(t, root, "old", true, `{"type":"session.start","id":"e1","data":{"copilotVersion":"0.0.350"}}`)); ok {
		t.Error("a start without a context has no directory")
	}
	if _, _, ok := SessionHead(filepath.Join(root, "missing.jsonl")); ok {
		t.Error("a missing log has no directory")
	}
}
