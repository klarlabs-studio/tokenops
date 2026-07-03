package jsonlfmt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/formatter"
)

// writeSession writes a synthetic Claude Code JSONL session under a project
// dir and returns the root.
func writeSession(t *testing.T, lines []map[string]any) string {
	t.Helper()
	root := t.TempDir()
	proj := filepath.Join(root, "-Users-x-proj", "nested") // deliberately nested
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(proj, "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	for _, l := range lines {
		if err := enc.Encode(l); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func msg(role string, content ...any) map[string]any {
	return map[string]any{"message": map[string]any{"role": role, "content": content}}
}
func toolUse(id, name string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}
func toolResult(id, content string) map[string]any {
	return map[string]any{"type": "tool_result", "tool_use_id": id, "content": content}
}
func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

const goTestOut = "=== RUN   TestA\n--- PASS: TestA (0.0s)\n=== RUN   TestB\n--- FAIL: TestB (0.1s)\n    b_test.go:9: boom\nFAIL\tpkg\t0.1s\nok  \tother\t0.2s\n"

func TestScan_CompositionAndCdAttribution(t *testing.T) {
	root := writeSession(t, []map[string]any{
		// assistant runs `cd /repo && go test` — output must attribute to "go", not "cd".
		msg("assistant", toolUse("u1", "Bash", map[string]any{"command": "cd /repo && go test ./..."})),
		msg("user", toolResult("u1", goTestOut)),
		// a Read tool result (file content) — counts under Read.
		msg("assistant", toolUse("u2", "Read", map[string]any{"file_path": "/x"})),
		msg("user", toolResult("u2", "line1\nline2\nline3\n")),
		// assistant prose.
		msg("assistant", text("Here is a fairly long explanation of what happened.")),
	})

	rep, records, err := Scan(formatter.DefaultFormatters(), Options{Root: root}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if rep.SessionsScanned != 1 {
		t.Fatalf("nested session not found (recursive walk broken): scanned=%d", rep.SessionsScanned)
	}
	if rep.Composition.ByTool["Bash"] == 0 || rep.Composition.ByTool["Read"] == 0 {
		t.Errorf("composition missing Bash/Read: %+v", rep.Composition.ByTool)
	}
	if rep.Composition.AssistantProse == 0 {
		t.Error("assistant prose not counted")
	}

	// The go-test output must be attributed to the go formatter (dedicated),
	// NOT to "cd". This is the compound-command attribution fix.
	var goROI *CommandROI
	for i := range rep.Commands {
		if rep.Commands[i].Command == "go" {
			goROI = &rep.Commands[i]
		}
		if rep.Commands[i].Command == "cd" {
			t.Errorf("output misattributed to cd: %+v", rep.Commands[i])
		}
	}
	if goROI == nil {
		t.Fatalf("no 'go' command ROI; commands=%+v", rep.Commands)
	}
	if !goROI.Handled {
		t.Error("go should be handled by a dedicated formatter")
	}
	if goROI.SavedBalanced <= 0 {
		t.Error("go test output should compress at balanced")
	}

	// A fmtlearn record should have been synthesised for the go command.
	var sawGo bool
	for _, r := range records {
		if r.Command == "go" && r.Handled {
			sawGo = true
		}
	}
	if !sawGo {
		t.Error("no fmtlearn record for go")
	}
}

func TestSplitChain_And_BashToken(t *testing.T) {
	cases := map[string]string{
		`cd /repo && go test ./...`:      "go",
		`cd a; npm install`:              "npm",
		`export X=1 && /usr/bin/git log`: "git",
		`cd only`:                        "", // nothing but cd -> no real command
		`sudo docker ps`:                 "docker",
		`FOO=bar pytest -q`:              "pytest",
	}
	for cmd, want := range cases {
		in, _ := json.Marshal(map[string]string{"command": cmd})
		if got := bashCommandToken(in); got != want {
			t.Errorf("bashCommandToken(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestScan_ReadReReadAndDup(t *testing.T) {
	body := "package main\n\nfunc main() {}\n"
	root := writeSession(t, []map[string]any{
		// same file read 3 times in the session -> 2 re-reads wasted.
		msg("assistant", toolUse("r1", "Read", map[string]any{"file_path": "/repo/main.go"})),
		msg("user", toolResult("r1", body)),
		msg("assistant", toolUse("r2", "Read", map[string]any{"file_path": "/repo/main.go"})),
		msg("user", toolResult("r2", body)),
		msg("assistant", toolUse("r3", "Read", map[string]any{"file_path": "/repo/main.go", "offset": 1, "limit": 2})),
		msg("user", toolResult("r3", body)),
		// a different file, read once, ranged.
		msg("assistant", toolUse("r4", "Read", map[string]any{"file_path": "/repo/util.go", "limit": 50})),
		msg("user", toolResult("r4", "package util\n")),
	})
	rep, _, err := Scan(formatter.DefaultFormatters(), Options{Root: root}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	rr := rep.Reads
	if rr.Reads != 4 {
		t.Fatalf("reads = %d, want 4", rr.Reads)
	}
	if rr.RangedReads != 2 { // r3 + r4
		t.Errorf("ranged = %d, want 2", rr.RangedReads)
	}
	// main.go read 3x -> 2 re-reads wasted (2 * len(body)).
	wantWaste := int64(2 * len(body))
	if rr.RepeatReadBytes != wantWaste {
		t.Errorf("repeat read bytes = %d, want %d", rr.RepeatReadBytes, wantWaste)
	}
	// byte-identical body seen 3x -> 2 duplicate copies.
	if rr.DupContentBytes != int64(2*len(body)) {
		t.Errorf("dup content bytes = %d, want %d", rr.DupContentBytes, 2*len(body))
	}
	if len(rr.TopReReads) != 1 || rr.TopReReads[0].Path != "/repo/main.go" || rr.TopReReads[0].Reads != 3 {
		t.Errorf("top re-read wrong: %+v", rr.TopReReads)
	}
	if rr.ByExt[".go"] == 0 {
		t.Error("by-ext missing .go")
	}
}

func TestScan_NoLogsIsEmpty(t *testing.T) {
	rep, _, err := Scan(formatter.DefaultFormatters(), Options{Root: t.TempDir()}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if rep.SessionsScanned != 0 || len(rep.Commands) != 0 {
		t.Errorf("empty root should yield empty report: %+v", rep)
	}
}
