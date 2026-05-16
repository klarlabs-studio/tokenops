package codexjsonl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleSessionMeta = `{"timestamp":"2026-04-17T08:44:43.195Z","type":"session_meta","payload":{"id":"session-abc","model_provider":"openai"}}`

const sampleTokenCountWithUsage = `{"timestamp":"2026-04-17T08:45:14.725Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":31220,"cached_input_tokens":18816,"output_tokens":289,"reasoning_output_tokens":26,"total_tokens":31509},"last_token_usage":{"input_tokens":31220,"cached_input_tokens":18816,"output_tokens":289,"reasoning_output_tokens":26,"total_tokens":31509},"model_context_window":258400},"rate_limits":{"primary":{"used_percent":10.0,"window_minutes":300,"resets_at":1776431887},"secondary":{"used_percent":2.0,"window_minutes":10080,"resets_at":1777018687},"plan_type":"plus"}}}`

const sampleTokenCountRateLimitsOnly = `{"timestamp":"2026-04-17T08:45:08.123Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":{"used_percent":10.0,"window_minutes":300,"resets_at":1776431887},"secondary":{"used_percent":2.0,"window_minutes":10080,"resets_at":1777018687},"plan_type":"plus"}}}`

// ReadFile must yield a Turn for token_count records with usage data,
// skip the initial rate-limits-only emit, ignore non-token_count
// event_msg records, and pick up the session ID from session_meta.
func TestReadFileSkipsRateLimitOnlyEmitsAndKeepsUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-x.jsonl")
	body := strings.Join([]string{
		sampleSessionMeta,
		`{"timestamp":"2026-04-17T08:45:08.000Z","type":"event_msg","payload":{"type":"agent_message","payload":{"x":1}}}`,
		sampleTokenCountRateLimitsOnly,
		sampleTokenCountWithUsage,
		`not even json`,
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var turns []Turn
	if err := ReadFile(path, func(t Turn) error { turns = append(turns, t); return nil }); err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("want 1 emitted turn; got %d", len(turns))
	}
	tr := turns[0]
	if tr.SessionID != "session-abc" {
		t.Errorf("SessionID = %q", tr.SessionID)
	}
	if tr.InputTokens != 31220 || tr.OutputTokens != 289 {
		t.Errorf("token counts: in=%d out=%d", tr.InputTokens, tr.OutputTokens)
	}
	if tr.RateLimits.PlanType != "plus" {
		t.Errorf("plan_type = %q", tr.RateLimits.PlanType)
	}
	if tr.RateLimits.PrimaryUsedPercent != 10.0 {
		t.Errorf("primary used_percent = %v", tr.RateLimits.PrimaryUsedPercent)
	}
	if tr.RateLimits.SecondaryWindowMinutes != 10080 {
		t.Errorf("secondary window_minutes = %d", tr.RateLimits.SecondaryWindowMinutes)
	}
	if tr.RecordSequence != 1 {
		t.Errorf("RecordSequence = %d; want 1", tr.RecordSequence)
	}
}

// FindSessionFiles must walk the yyyy/mm/dd/ nested layout.
func TestFindSessionFilesWalksNestedLayout(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "2026", "04", "17")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "rollout-a.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "rollout-b.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "not-a-rollout.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := FindSessionFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rollout files; got %d (%v)", len(got), got)
	}
}

func TestDefaultRoot(t *testing.T) {
	t.Setenv("HOME", filepath.FromSlash("/tmp/test-home"))
	p, err := DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p, filepath.Join(".codex", "sessions")) {
		t.Errorf("DefaultRoot = %q; should end in .codex/sessions", p)
	}
}
