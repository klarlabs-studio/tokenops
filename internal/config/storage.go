package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// StorageConfig configures the local event store. When Enabled, the
// daemon opens a sqlite database at Path and emits PromptEvents into it
// via an async bus.
type StorageConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
}

// RetentionConfig is an opt-in event-store prune. Empty Keep disables
// the scheduler entirely so a fresh install never deletes events. The
// daemon starts the worker only when at least one keep window is
// positive. Audit log rows are never pruned.
type RetentionConfig struct {
	// Interval is how often the pruner wakes. Zero defaults to 1h when
	// the scheduler is running.
	Interval time.Duration `yaml:"interval,omitempty"`
	// Keep maps an event type (prompt, workflow, optimization,
	// coaching, rule_source, rule_analysis) to a window. Values accept
	// Go durations plus a "d" day suffix (30d = 720h).
	Keep map[string]string `yaml:"keep,omitempty"`
	// KeepBySource maps a source ("opencode", "codex-jsonl",
	// "claude-code-jsonl", ...) to its own window, overriding the
	// type window for that source's rows.
	//
	// Every vendor-usage reader writes type "prompt", so keep alone
	// cannot tell one client from another: a window short enough for the
	// live stream also deletes the imported history of a client last
	// used months ago, which is exactly what a backfill consists of.
	// A source entry of "forever" (or "never", or "0") keeps that
	// source's rows indefinitely.
	//
	// Keys may be qualified with an event type ("prompt:opencode") when a
	// source emits more than one; a bare source name applies to prompt
	// events, which is what every usage reader writes.
	KeepBySource map[string]string `yaml:"keep_by_source,omitempty"`
	// Reclaim runs a VACUUM after a prune that deleted rows so the freed
	// pages return to the filesystem. Without it SQLite keeps them on
	// its freelist and the database file never shrinks — pruning frees
	// space the operator cannot see. Costs a full rewrite under a write
	// lock, so it is opt-in.
	Reclaim bool `yaml:"reclaim,omitempty"`
}

// Enabled reports whether any keep window is set so the daemon should
// start the pruner.
func (c RetentionConfig) Enabled() bool {
	for _, raw := range c.Keep {
		d, err := ParseKeepDuration(raw)
		if err == nil && d > 0 {
			return true
		}
	}
	// A source window alone is enough to want the pruner running: it may
	// be the only rule, and it may be shorter than any type window.
	for _, raw := range c.KeepBySource {
		d, err := ParseKeepDuration(raw)
		if err == nil && d > 0 {
			return true
		}
	}
	return false
}

// knownRetentionTypes are the event types the pruner accepts. audit_log
// is intentionally absent — operators rely on it for forensic queries.
var knownRetentionTypes = map[string]struct{}{
	"prompt":        {},
	"workflow":      {},
	"optimization":  {},
	"coaching":      {},
	"rule_source":   {},
	"rule_analysis": {},
}

// ParseKeepDuration accepts Go durations plus a day suffix ("30d").
func ParseKeepDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" || s == "0s" {
		return 0, nil
	}
	// A zero window means "never prune this". Spelling it out keeps a
	// config from being read as "keep for zero time", which in a delete
	// path is the opposite of what it does.
	switch strings.ToLower(s) {
	case "forever", "never":
		return 0, nil
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid day duration %q", s)
		}
		if n < 0 {
			return 0, fmt.Errorf("duration must be non-negative, got %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("duration must be non-negative, got %s", s)
	}
	return d, nil
}

// SplitRetentionSourceKey splits a keep_by_source key into its event type
// and source. A bare source name means prompt events, which is what every
// vendor-usage reader writes.
func SplitRetentionSourceKey(key string) (eventType, source string) {
	if typ, src, ok := strings.Cut(key, ":"); ok {
		return strings.TrimSpace(typ), strings.TrimSpace(src)
	}
	return "prompt", strings.TrimSpace(key)
}
