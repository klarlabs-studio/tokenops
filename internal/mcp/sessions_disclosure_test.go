package mcp

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The consolidated tokenops_sessions tool promises derived figures by
// default. These sentinels stand in for what must not cross that line: the
// operator's instruction text, the absolute paths of the files an agent
// edited, and the free-form detail of where work went sideways. They are
// fictional.
const (
	sentinelInstruction = "quokka-lantern-7741 rewrite the parser"
	sentinelPath        = "/Users/fictional-operator/lantern-7741/parser.go"
	sentinelOtherPath   = "/Users/fictional-operator/lantern-7741/lexer.go"
	sentinelFriction    = "edited the same file twice"
)

// disclosureServer is the consolidated server over a transcript carrying
// every sentinel: an instruction typed twice (a repeat the prompt scorer
// quotes), a task that returns to a file after moving on (rework, whose
// detail is prose), and a rejection.
func disclosureServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	edit := func(ts, path string) string {
		return `{"type":"assistant","timestamp":"` + ts + `","sessionId":"s","message":{"usage":{"input_tokens":100},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` + path + `"}}]}}`
	}
	lines := []string{
		`{"type":"user","timestamp":"2099-01-01T10:00:00Z","sessionId":"s","message":{"content":"` + sentinelInstruction + `"}}`,
		edit("2099-01-01T10:00:01Z", sentinelPath),
		edit("2099-01-01T10:00:02Z", sentinelOtherPath),
		edit("2099-01-01T10:00:03Z", sentinelPath),
		`{"type":"user","timestamp":"2099-01-01T10:01:00Z","sessionId":"s","message":{"content":"` + sentinelInstruction + `"}}`,
		edit("2099-01-01T10:01:01Z", sentinelPath),
		`{"type":"user","timestamp":"2099-01-01T10:02:00Z","sessionId":"s","message":{"content":"nope that's wrong, quokka-lantern-7741 again"}}`,
		edit("2099-01-01T10:02:01Z", sentinelPath),
	}
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, err := range []error{
		RegisterStoryTools(srv, StoryDeps{Root: root}),
		RegisterCoachTools(srv, CoachDeps{JSONLRoot: root}),
		RegisterAgentDXTools(srv, AgentDXDeps{Root: root}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := Consolidate(srv); err != nil {
		t.Fatal(err)
	}
	return srv
}

func sessionsCall(t *testing.T, srv *Server, args map[string]any) (map[string]any, string) {
	t.Helper()
	out, err := callPublic(t, srv, "tokenops_sessions", args)
	if err != nil {
		t.Fatalf("tokenops_sessions %v: %v", args, err)
	}
	raw, _ := json.Marshal(out)
	return out, string(raw)
}

var sentinels = []string{"quokka-lantern-7741", "/Users/fictional-operator", sentinelFriction}

// By default the story and prompts views carry derived figures only, as
// the tool's description says: none of the sentinels comes back.
func TestSessionsDefaultWithholdsInstructionsPathsAndFrictionText(t *testing.T) {
	srv := disclosureServer(t)
	for _, view := range []string{"story", "prompts"} {
		args := map[string]any{"view": view}
		if view == "story" {
			args["all"] = true
		} else {
			args["since"] = "2000-01-01T00:00:00Z"
		}
		out, raw := sessionsCall(t, srv, args)
		for _, s := range sentinels {
			if strings.Contains(raw, s) {
				t.Errorf("view %s by default returned %q:\n%s", view, s, raw)
			}
		}
		if out[view] == nil {
			t.Fatalf("view %s: no payload: %s", view, raw)
		}
	}

	out, raw := sessionsCall(t, srv, map[string]any{"view": "story", "all": true})
	story, _ := out["story"].(map[string]any)
	if story["titles_withheld"] != true || story["paths_withheld"] != true {
		t.Errorf("story does not say what it withheld: %s", raw)
	}
	tasks, _ := story["tasks"].([]any)
	if len(tasks) == 0 {
		t.Fatalf("no tasks; the assertions above would be vacuous: %s", raw)
	}
	// What remains is the shape of the work: how many files, which kinds
	// of friction.
	task, _ := tasks[len(tasks)-1].(map[string]any)
	if n, _ := task["files_edited"].(float64); n < 2 {
		t.Errorf("files_edited = %v, want the count of files the task touched: %s", task["files_edited"], raw)
	}
	kinds, _ := task["frictions"].([]any)
	if len(kinds) == 0 || kinds[0] != "rework" {
		t.Errorf("frictions = %v, want enumerated kinds such as rework", task["frictions"])
	}

	out, raw = sessionsCall(t, srv, map[string]any{"view": "prompts", "since": "2000-01-01T00:00:00Z"})
	prompts, _ := out["prompts"].(map[string]any)
	if n, _ := prompts["total_prompts"].(float64); n == 0 {
		t.Errorf("prompts scored nothing; the assertions above would be vacuous: %s", raw)
	}
}

// quote: true is the explicit opt-in to the narrative: the instruction
// text, the local paths and the friction detail all come back.
func TestSessionsQuoteReturnsTheNarrative(t *testing.T) {
	srv := disclosureServer(t)
	_, raw := sessionsCall(t, srv, map[string]any{"view": "story", "all": true, "quote": true})
	for _, s := range sentinels {
		if !strings.Contains(raw, s) {
			t.Errorf("story with quote: true is missing %q:\n%s", s, raw)
		}
	}
	if strings.Contains(raw, "titles_withheld") || strings.Contains(raw, "paths_withheld") {
		t.Errorf("a quoted story still says it withheld something: %s", raw)
	}
	_, raw = sessionsCall(t, srv, map[string]any{"view": "prompts", "since": "2000-01-01T00:00:00Z", "quote": true})
	if !strings.Contains(raw, sentinels[0]) {
		t.Errorf("prompts with quote: true is missing the instruction text:\n%s", raw)
	}
}

// The opt-in is stated before invocation: the parameter's schema says it
// returns the operator's instruction text and local paths, and the tool's
// description no longer promises a withholding the opt-in breaks.
func TestSessionsQuoteIsDescribedBeforeInvocation(t *testing.T) {
	srv := disclosureServer(t)
	for _, ti := range srv.Tools() {
		if ti.Name != "tokenops_sessions" {
			continue
		}
		b, _ := json.Marshal(ti)
		var def struct {
			Description string `json:"description"`
			InputSchema struct {
				Properties map[string]struct {
					Description string `json:"description"`
				} `json:"properties"`
			} `json:"inputSchema"`
		}
		if err := json.Unmarshal(b, &def); err != nil {
			t.Fatal(err)
		}
		q := def.InputSchema.Properties["quote"].Description
		for _, want := range []string{"instruction text", "local file paths"} {
			if !strings.Contains(q, want) {
				t.Errorf("quote's description %q does not state it returns %s", q, want)
			}
		}
		if !strings.Contains(def.Description, "quote") {
			t.Errorf("description does not name the opt-in: %q", def.Description)
		}
		return
	}
	t.Fatal("tokenops_sessions is not listed")
}

// A view that cannot quote refuses the opt-in rather than ignoring it.
func TestSessionsQuoteDoesNotApplyToDX(t *testing.T) {
	srv := disclosureServer(t)
	if _, err := callPublic(t, srv, "tokenops_sessions", map[string]any{"view": "dx", "quote": true}); err == nil {
		t.Error("dx accepted quote, which it has nothing to quote with")
	}
}
