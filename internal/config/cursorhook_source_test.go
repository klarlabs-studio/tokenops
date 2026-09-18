package config

import (
	"context"
	"testing"
	"time"
)

// The cursor-turns poller runs unconditionally — it reads a ledger the
// coach hook writes, so it costs nothing when the hook is not installed.
// That also meant it appeared in no source registry: invisible to
// `vendor-usage status`, never staleness-checked, and impossible to name in
// a retention rule without guessing its tag.
func TestCursorHookIsARegisteredSource(t *testing.T) {
	var found *VendorUsageSource
	for i, s := range (Config{}).VendorUsageSources() {
		if s.SourceTag == "cursor-hook" {
			found = &(Config{}).VendorUsageSources()[i]
		}
	}
	if found == nil {
		t.Fatal("cursor-hook is not in VendorUsageSources; it writes events but nothing lists it")
	}
	if !found.AlwaysOn {
		t.Error("the cursor turn poller runs unconditionally, so it is AlwaysOn")
	}
	if found.Enabled {
		t.Error("nothing in config enables it, so Enabled stays false — a fresh config still switches nothing on")
	}
}

// Registering it for visibility must not add it to the staleness check:
// the ledger is empty until the Cursor coach hook is installed, so every
// operator who does not run Cursor would get a permanent warning — the
// exact failure the staleness check was just corrected for. Visibility and
// alarming are different jobs.
func TestCursorHookIsNotStaleChecked(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	counter := &fakeSeer{counts: map[string]int64{}, last: map[string]time.Time{}}
	stale, err := (Config{}).CheckStaleIngestion(context.Background(), counter, nil, StaleIngestionWindow, now)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, s := range stale {
		if s.SourceTag == "cursor-hook" {
			t.Fatal("an always-on source must not be staleness-checked on a fresh config")
		}
	}
}

// A keep_by_source key naming a source that has never written an event is
// doing nothing. The live config on the machine that prompted this said
// `cursor-turns: forever` — the directory name, not the tag, which is
// `cursor-hook` — so the operator believed their Cursor history was pinned
// and it was on the default 120-day window.
func TestUnmatchedRetentionKeyIsReported(t *testing.T) {
	c := Config{}
	c.Retention.KeepBySource = map[string]string{
		"cursor-turns":      "forever",
		"claude-code-jsonl": "forever",
	}
	seen := map[string]int64{"claude-code-jsonl": 12, "cursor-hook": 2}
	unmatched := c.UnmatchedRetentionSources(seen)
	if len(unmatched) != 1 || unmatched[0] != "cursor-turns" {
		t.Fatalf("unmatched = %v, want [cursor-turns]", unmatched)
	}
}

// A key qualified with an event type still resolves to its source half.
func TestQualifiedRetentionKeyIsMatchedOnItsSource(t *testing.T) {
	c := Config{}
	c.Retention.KeepBySource = map[string]string{"prompt:opencode": "forever"}
	if got := c.UnmatchedRetentionSources(map[string]int64{"opencode": 1}); len(got) != 0 {
		t.Fatalf("unmatched = %v, want none", got)
	}
}

// A source that is configured but has not written yet must not be called a
// mistake — that is a fresh install, not a typo.
func TestKnownButUnwrittenSourceIsNotUnmatched(t *testing.T) {
	c := Config{}
	c.VendorUsage.OpenCode.Enabled = true
	c.Retention.KeepBySource = map[string]string{"opencode": "forever"}
	if got := c.UnmatchedRetentionSources(map[string]int64{}); len(got) != 0 {
		t.Fatalf("unmatched = %v, want none for a configured source", got)
	}
}
