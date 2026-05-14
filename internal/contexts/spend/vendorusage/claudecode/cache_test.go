package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Read accepts the real cache schema (version 3 observed). Unknown
// fields in the JSON must be ignored so the reader stays
// forward-compatible when Anthropic adds keys.
func TestReadParsesObservedSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats-cache.json")
	body := `{
		"version": 3,
		"lastComputedDate": "2026-05-06",
		"dailyActivity": [
			{"date": "2026-05-01", "messageCount": 40844, "sessionCount": 8, "toolCallCount": 15046},
			{"date": "2026-05-02", "messageCount": 26203, "sessionCount": 9, "toolCallCount": 11054}
		],
		"dailyModelTokens": [
			{"date": "2026-05-01", "tokensByModel": {"claude-opus-4-7": 17871443, "claude-haiku-4-5-20251001": 57540}}
		],
		"modelUsage": {
			"claude-opus-4-7": {
				"inputTokens": 704209,
				"outputTokens": 57141125,
				"cacheReadInputTokens": 27373654419,
				"cacheCreationInputTokens": 449066536,
				"webSearchRequests": 0,
				"costUSD": 0,
				"unknownFutureField": "ignored"
			}
		},
		"totalSessions": 350,
		"totalMessages": 553443,
		"firstSessionDate": "2025-12-24T15:33:11.378Z"
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if c.Version != 3 {
		t.Errorf("version = %d", c.Version)
	}
	if got := len(c.DailyActivity); got != 2 {
		t.Errorf("dailyActivity rows = %d", got)
	}
	if got := c.ModelUsage["claude-opus-4-7"].InputTokens; got != 704209 {
		t.Errorf("input tokens = %d", got)
	}
}

// Read must return os.ErrNotExist when the cache is missing so
// callers can distinguish "Claude Code not installed" from a parse
// failure.
func TestReadMissingFileReturnsNotExist(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), "absent.json"))
	if !os.IsNotExist(err) {
		t.Fatalf("want os.ErrNotExist; got %v", err)
	}
}

// Read must surface the path in the parse error so logs point at the
// offending file.
func TestReadInvalidJSONErrorIncludesPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q should contain path %q", err, path)
	}
}

// ActivityForDate must find the matching row, return false otherwise.
// TokensForDate behaves the same; covering both keeps the per-day
// lookup contract pinned.
func TestActivityAndTokensForDate(t *testing.T) {
	c := &StatsCache{
		DailyActivity: []DailyActivity{
			{Date: "2026-05-01", MessageCount: 100, SessionCount: 1, ToolCallCount: 10},
		},
		DailyModelTokens: []DailyModelTokens{
			{Date: "2026-05-01", TokensByModel: map[string]int64{"claude-opus-4-7": 12345}},
		},
	}
	if a, ok := c.ActivityForDate("2026-05-01"); !ok || a.MessageCount != 100 {
		t.Errorf("activity hit: ok=%v row=%+v", ok, a)
	}
	if _, ok := c.ActivityForDate("2026-05-02"); ok {
		t.Errorf("activity miss should return false")
	}
	if tk := c.TokensForDate("2026-05-01"); tk["claude-opus-4-7"] != 12345 {
		t.Errorf("token map: %v", tk)
	}
	if tk := c.TokensForDate("2026-05-02"); tk != nil {
		t.Errorf("missing date should return nil, got %v", tk)
	}
}

// DefaultPath must land under ~/.claude/stats-cache.json on real
// machines. Hard-coding the HOME via the env so the test runs in
// any sandbox.
func TestDefaultPath(t *testing.T) {
	t.Setenv("HOME", filepath.FromSlash("/tmp/test-home"))
	p, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := filepath.Join(".claude", "stats-cache.json")
	if !strings.HasSuffix(p, wantSuffix) {
		t.Errorf("path %q missing suffix %q", p, wantSuffix)
	}
}
