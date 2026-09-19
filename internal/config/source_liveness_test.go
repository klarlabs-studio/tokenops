package config

import (
	"context"
	"testing"
	"time"
)

func jsonlCfg() Config {
	c := Config{}
	c.VendorUsage.CodexJSONL.Enabled = true
	return c
}

// The check could only see "no events lately", so a vendor the operator
// simply had not used looked exactly like a poller that had died. Two of
// three enabled sources on one machine sat permanently at [critical] while
// both readers were provably complete — which trains the operator to ignore
// the alarm built after a 27-day outage.
func TestCaughtUpSourceIsNotStale(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	lastIngest := now.AddDate(0, 0, -41)
	counter := &fakeSeer{
		counts: map[string]int64{},
		last:   map[string]time.Time{"codex-jsonl": lastIngest},
	}
	// The source itself has produced nothing since that same moment.
	probes := map[string]SourceProbe{
		"codex-jsonl": func() (time.Time, bool) { return lastIngest, true },
	}
	stale, err := jsonlCfg().CheckStaleIngestion(context.Background(), counter, probes, StaleIngestionWindow, now)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if staleFor(stale, "codex-jsonl") {
		t.Fatalf("a fully-ingested source is not stale, got %+v", stale)
	}
}

// Behind the source is the real incident: data exists and we have not read it.
func TestSourceAheadOfIngestIsStale(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	counter := &fakeSeer{
		counts: map[string]int64{},
		last:   map[string]time.Time{"codex-jsonl": now.AddDate(0, 0, -41)},
	}
	probes := map[string]SourceProbe{
		// The client wrote a transcript yesterday; we did not ingest it.
		"codex-jsonl": func() (time.Time, bool) { return now.AddDate(0, 0, -1), true },
	}
	stale, err := jsonlCfg().CheckStaleIngestion(context.Background(), counter, probes, StaleIngestionWindow, now)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !staleFor(stale, "codex-jsonl") {
		t.Fatalf("a source ahead of ingestion is stale, got %+v", stale)
	}
}

// An unreadable or absent origin is an unknown, not a clean bill of health,
// so the time-based warning stands.
func TestUnknownProbeKeepsTheTimeBasedWarning(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	counter := &fakeSeer{
		counts: map[string]int64{},
		last:   map[string]time.Time{"codex-jsonl": now.AddDate(0, 0, -41)},
	}
	probes := map[string]SourceProbe{
		"codex-jsonl": func() (time.Time, bool) { return time.Time{}, false },
	}
	stale, err := jsonlCfg().CheckStaleIngestion(context.Background(), counter, probes, StaleIngestionWindow, now)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !staleFor(stale, "codex-jsonl") {
		t.Fatalf("an unknown origin must not read as caught up, got %+v", stale)
	}
}

// No probe at all is the remote-poller case: an enabled cookie or admin-API
// poller that has ingested nothing IS broken, because the vendor endpoint
// always has an answer. Behaviour there is unchanged.
func TestNoProbeKeepsTheExistingBehaviour(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	c := Config{}
	c.VendorUsage.ClaudeUsageMeter.Enabled = true
	counter := &fakeSeer{
		counts: map[string]int64{},
		last:   map[string]time.Time{"claude-usage-meter": now.AddDate(0, 0, -41)},
	}
	stale, err := c.CheckStaleIngestion(context.Background(), counter, nil, StaleIngestionWindow, now)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !staleFor(stale, "claude-usage-meter") {
		t.Fatalf("a remote poller with no events is still stale, got %+v", stale)
	}
}

// Nothing ingested and nothing at the origin means there is nothing to do —
// a freshly enabled reader for a client the operator has not run yet.
func TestNeverIngestedWithAnEmptySourceIsNotStale(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	counter := &fakeSeer{counts: map[string]int64{}, last: map[string]time.Time{}}
	probes := map[string]SourceProbe{
		"codex-jsonl": func() (time.Time, bool) { return time.Time{}, true },
	}
	stale, err := jsonlCfg().CheckStaleIngestion(context.Background(), counter, probes, StaleIngestionWindow, now)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if staleFor(stale, "codex-jsonl") {
		t.Fatalf("an empty origin has nothing to ingest, got %+v", stale)
	}
}

// Never ingested but the origin has data is the worst case: a reader that
// has never worked at all.
func TestNeverIngestedWithDataAtSourceIsStale(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	counter := &fakeSeer{counts: map[string]int64{}, last: map[string]time.Time{}}
	probes := map[string]SourceProbe{
		"codex-jsonl": func() (time.Time, bool) { return now.Add(-time.Hour), true },
	}
	stale, err := jsonlCfg().CheckStaleIngestion(context.Background(), counter, probes, StaleIngestionWindow, now)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !staleFor(stale, "codex-jsonl") {
		t.Fatalf("a reader that never ingested existing data is stale, got %+v", stale)
	}
}

// staleFor scopes an assertion to one source. The checks run over every
// enabled source, so asserting on the length of the whole result couples a
// test about codex-jsonl to whatever else happens to be registered.
func staleFor(stale []StaleSource, tag string) bool {
	for _, s := range stale {
		if s.SourceTag == tag {
			return true
		}
	}
	return false
}
