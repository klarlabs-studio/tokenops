package teamshare

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
)

// factsTranscript is a Claude Code session whose instructions exercise
// what the upload derives from words: new work of several kinds, bare
// continuations, a rejection and a harness command.
func factsTranscript(t *testing.T, root string, start time.Time) {
	t.Helper()
	prompts := []string{
		"fix the login bug in the auth handler", "continue", "go",
		"no, that's wrong, revert it", "refactor the parser into two files",
		"add tests for the parser", "/compact", "ok", "write the release notes",
		"explain why the cache misses",
	}
	// Two-hour pauses before a continuation ("go") and before new work
	// ("refactor"): the first stays in its task, the second opens one.
	paused := map[int]bool{2: true, 4: true}
	var b strings.Builder
	at := start
	for i, p := range prompts {
		at = at.Add(7 * time.Minute)
		if paused[i] {
			at = at.Add(2 * time.Hour)
		}
		ts := func(d time.Duration) string { return at.Add(d).Format(time.RFC3339Nano) }
		text, _ := json.Marshal(p)
		fmt.Fprintf(&b, `{"type":"user","timestamp":%q,"sessionId":"s1","message":{"content":%s}}`+"\n", ts(0), text)
		fmt.Fprintf(&b, `{"type":"assistant","timestamp":%q,"sessionId":"s1","message":{"model":"claude-opus-5-5","usage":{"input_tokens":100},"content":[{"type":"text","text":"ok"},{"type":"tool_use","name":"Edit","input":{"file_path":"/r/f.go"}}]}}`+"\n", ts(time.Minute))
		fmt.Fprintf(&b, `{"type":"user","timestamp":%q,"sessionId":"s1","message":{"content":[{"type":"tool_result"}]}}`+"\n", ts(2*time.Minute))
	}
	path := filepath.Join(root, "proj", "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The upload read through the prompt-facts cache is exactly the upload
// read with the words: what it needs from them (does an instruction
// continue the work in flight, what kind of work it starts) is derived at
// read time, and the words are not kept.
func TestUploadFromPromptFactsMatchesUploadFromText(t *testing.T) {
	root := t.TempDir()
	factsTranscript(t, root, now.Add(-6*time.Hour))
	win := sessions.Window{Root: root, Source: "claude-code", IncludeScratch: true, Days: 14}
	cache := sessions.NewPromptFactsCache()

	build := func(units func(_, _ time.Time) ([]sessions.Unit, error)) []byte {
		t.Helper()
		d := deps()
		d.Units = units
		u, notes := Build(context.Background(), d, Options{Days: 14, RepoNames: RepoRemote, ClientVersion: "0.119.0"}, now)
		if len(notes.All()) > 0 {
			t.Fatalf("notes: %v", notes.All())
		}
		u.BatchID = "" // a fresh id per upload
		raw, err := json.Marshal(u)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var textUnits []sessions.Unit
	fromText := build(func(_, _ time.Time) ([]sessions.Unit, error) {
		us, err := sessions.Units(win, now)
		textUnits = us
		return us, err
	})
	var factUnits []sessions.Unit
	fromFacts := build(func(_, _ time.Time) ([]sessions.Unit, error) {
		w := win
		w.Cache = cache
		us, err := sessions.Units(w, now)
		factUnits = us
		return us, err
	})
	if string(fromText) != string(fromFacts) {
		t.Fatalf("uploads differ:\ntext  %s\nfacts %s", fromText, fromFacts)
	}
	if len(factUnits) == 0 {
		t.Fatal("no units read")
	}
	for _, u := range factUnits {
		if u.Prompt != "" {
			t.Fatalf("a prompt-facts read carries the words %q", u.Prompt)
		}
	}
	// Story boundaries, which the schema 3 units section groups by, are
	// the same from facts as from words.
	bounds := func(us []sessions.Unit) []string {
		tasks := story.Group(us, story.Options{})
		out := make([]string, 0, len(tasks))
		for _, task := range tasks {
			out = append(out, fmt.Sprintf("%s..%s/%d", task.Start.Format(time.TimeOnly), task.End.Format(time.TimeOnly), len(task.Units)))
		}
		return out
	}
	if a, b := bounds(textUnits), bounds(factUnits); !reflect.DeepEqual(a, b) || len(a) != 2 {
		t.Errorf("story boundaries from words %v, from facts %v; want the same, split once (two tasks)", a, b)
	}
	if cache.Len() != 1 {
		t.Errorf("cache holds %d files, want the transcript", cache.Len())
	}
}
