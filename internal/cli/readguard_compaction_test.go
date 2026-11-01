package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/infra/readguard"
)

// The hook reads the session's transcript, so a re-read after /compact is
// let through even though the file is unchanged.
func TestReadGuardHookAllowsTheReReadAfterACompaction(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	file := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(t.TempDir(), "s1.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"user","timestamp":"2026-11-01T10:00:00Z"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{
		"session_id": "s1", "transcript_path": transcript, "tool_name": "Read",
		"tool_input": map[string]any{"file_path": file},
	})
	run := func() string {
		t.Helper()
		cmd := &cobra.Command{}
		var out bytes.Buffer
		cmd.SetIn(bytes.NewReader(payload))
		cmd.SetOut(&out)
		if err := runReadGuardHook(cmd, readguard.ModeActive, dir, nil); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	run()
	if out := run(); !strings.Contains(out, `"deny"`) {
		t.Fatalf("an unchanged re-read was not refused: %q", out)
	}
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// Later than any read above: the reads ran at the wall clock.
	at := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	_, _ = f.WriteString(`{"type":"system","subtype":"compact_boundary","timestamp":"` + at + `"}` + "\n")
	_ = f.Close()
	if out := run(); out != "" {
		t.Fatalf("the re-read after a compaction was refused: %q", out)
	}
}
