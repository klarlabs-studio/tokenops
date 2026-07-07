package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCoachTranscript writes a transcript jsonl carrying the given cache-read
// load and returns its path.
func writeCoachTranscript(t *testing.T, dir string, cacheRead int64, model string) string {
	t.Helper()
	line := `{"type":"assistant","message":{"model":"` + model +
		`","usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":3,"cache_read_input_tokens":` +
		itoa(cacheRead) + `}}}` + "\n"
	p := filepath.Join(dir, "t.jsonl")
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// itoa avoids importing strconv in the test for one call.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func runCoach(t *testing.T, stateDir, transcript, sessionID string) string {
	t.Helper()
	in := map[string]any{
		"session_id":       sessionID,
		"transcript_path":  transcript,
		"hook_event_name":  "Stop",
		"stop_hook_active": false,
	}
	body, _ := json.Marshal(in)
	var out bytes.Buffer
	root := NewRoot()
	root.SetArgs([]string{"coach-hook", "--dir", stateDir})
	root.SetIn(bytes.NewReader(body))
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("coach-hook: %v", err)
	}
	return out.String()
}

func TestCoachHook_NudgeEmitsSystemMessage(t *testing.T) {
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1_400_000, "claude-opus-4-8")
	out := runCoach(t, dir, tp, "s1")

	var got struct {
		SystemMessage  string `json:"systemMessage"`
		SuppressOutput bool   `json:"suppressOutput"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("expected systemMessage JSON, got %q (%v)", out, err)
	}
	if !got.SuppressOutput {
		t.Fatalf("suppressOutput must be true")
	}
	if !strings.Contains(got.SystemMessage, "cache-read") || !strings.Contains(got.SystemMessage, "/compact") {
		t.Fatalf("systemMessage should name the lever, got %q", got.SystemMessage)
	}
}

func TestCoachHook_BelowThresholdNoOutput(t *testing.T) {
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 100_000, "claude-opus-4-8")
	out := runCoach(t, dir, tp, "s2")
	if strings.TrimSpace(out) != "" {
		t.Fatalf("below threshold must produce no stdout, got %q", out)
	}
}

func TestCoachHook_MalformedStdinFailsOpen(t *testing.T) {
	var out bytes.Buffer
	root := NewRoot()
	root.SetArgs([]string{"coach-hook", "--dir", t.TempDir()})
	root.SetIn(strings.NewReader("not json"))
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("must fail open (no error), got %v", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("malformed input must produce no stdout, got %q", out.String())
	}
}
