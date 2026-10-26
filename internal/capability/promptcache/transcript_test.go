package promptcache_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/promptcache"
)

// transcript writes a Claude Code transcript whose last turn's request went
// out at sent, writing with the hour-long lifetime when hour is set.
func transcript(t *testing.T, sent time.Time, hour bool, trailing ...time.Time) string {
	t.Helper()
	h1 := 0
	if hour {
		h1 = 100
	}
	ts := func(at time.Time) string { return at.UTC().Format(time.RFC3339Nano) }
	lines := []string{
		`{"type":"user","timestamp":"` + ts(sent) + `","sessionId":"abc","message":{"content":"go"}}`,
		`{"type":"assistant","timestamp":"` + ts(sent.Add(30*time.Second)) + `","sessionId":"abc","message":{"id":"m1","model":"claude-opus-4-7","usage":{"input_tokens":1,"cache_read_input_tokens":900,"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_1h_input_tokens":` + strconv.Itoa(h1) + `}}}}`,
	}
	for _, at := range trailing {
		lines = append(lines, `{"type":"user","timestamp":"`+ts(at)+`","sessionId":"abc","message":{"content":[{"type":"tool_result"}]}}`)
	}
	path := filepath.Join(t.TempDir(), "abc.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The cache goes cold its lifetime after the last request went out: the
// prompt that started the last turn, or a tool result sent after it.
func TestCacheExpiryFromTheTranscript(t *testing.T) {
	sent := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		path string
		want time.Time
	}{
		{"five-minute cache", transcript(t, sent, false), sent.Add(5 * time.Minute)},
		{"hour cache", transcript(t, sent, true), sent.Add(time.Hour)},
		{"a request after the last reply", transcript(t, sent, false, sent.Add(2*time.Minute)), sent.Add(7 * time.Minute)},
	}
	for _, c := range cases {
		got, ok := promptcache.TranscriptExpiry(c.path)
		if !ok || !got.Equal(c.want) {
			t.Errorf("%s: expiry = %v, %v; want %v", c.name, got, ok, c.want)
		}
	}
	if _, ok := promptcache.TranscriptExpiry(""); ok {
		t.Error("an expiry with no transcript")
	}
	if _, ok := promptcache.TranscriptExpiry(filepath.Join(t.TempDir(), "missing.jsonl")); ok {
		t.Error("an expiry for a missing transcript")
	}
}
