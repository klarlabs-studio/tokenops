package fmtinsight

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
)

// session writes one Claude Code session running `mytool build` six
// times, past learn's five-run floor, and returns its projects root.
func session(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "-work-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	out := "building...\nnoise line\nnoise line\nERROR: boom\n"
	for i := range 6 {
		id := fmt.Sprintf("u%d", i)
		for _, l := range []map[string]any{
			{"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": "mytool build"}}}}},
			{"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": out}}}},
		} {
			if err := enc.Encode(l); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func mytool() config.CommandFmtConfig {
	var cfg config.CommandFmtConfig
	cfg.Formatters = []config.CommandFmtFormatter{{Command: "mytool", Critical: []string{"^ERROR"}}}
	cfg.Formatters[0].Drop.Balanced = []string{"^noise"}
	return cfg
}

// A command the operator wrote a formatter for is handled. The MCP tools
// read the built-in catalog alone and reported it unhandled, a formatter
// still to write.
func TestAnalyzeUsesTheConfiguredFormatters(t *testing.T) {
	root := session(t)
	handled := func(cfg config.CommandFmtConfig) bool {
		rep, err := Analyze(cfg, Window{Root: root}, time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range rep.Commands {
			if c.Command == "mytool" {
				return c.Handled
			}
		}
		t.Fatalf("mytool not in %+v", rep.Commands)
		return false
	}
	if !handled(mytool()) {
		t.Error("configured formatter not used")
	}
	if handled(config.CommandFmtConfig{}) {
		t.Error("mytool handled with no formatter configured")
	}
}

func TestLearnDoesNotProposeAConfiguredFormatter(t *testing.T) {
	root := session(t)
	proposed := func(cfg config.CommandFmtConfig) bool {
		rep, err := Learn(cfg, LearnOptions{RecoverDir: t.TempDir(), Sessions: &Window{Root: root}}, time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range rep.NextFormatters {
			if n.Command == "mytool" {
				return true
			}
		}
		return false
	}
	if proposed(mytool()) {
		t.Error("proposed writing a formatter mytool already has")
	}
	if !proposed(config.CommandFmtConfig{}) {
		t.Error("an unhandled mytool was not proposed")
	}
}
