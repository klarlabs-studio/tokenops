package readguard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func seedLedger(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return dir
}

// The guard has been reclaiming tokens without anything recording it, so
// TEU — Token Efficiency Uplift — reported N/A while real uplift was
// happening. Reclamations exposes the blocked reads so they can be
// published as optimization events.
func TestReclamationsReturnsBlockedReads(t *testing.T) {
	dir := seedLedger(t,
		`{"ts":"2026-08-23T10:00:00Z","mode":"active","session":"s1","path":"/a.go","action":"blocked","est_tokens":2110,"repeat":true}`,
		`{"ts":"2026-08-23T10:00:01Z","mode":"active","session":"s1","path":"/b.go","action":"allow","est_tokens":900}`,
		`{"ts":"2026-08-23T10:00:02Z","mode":"active","session":"s2","path":"/c.go","action":"blocked","est_tokens":500,"repeat":true}`,
	)
	got, err := Reclamations(dir)
	if err != nil {
		t.Fatalf("reclamations: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d reclamations, want 2 blocked reads: %+v", len(got), got)
	}
	if got[0].Tokens != 2110 || got[0].SessionID != "s1" || got[0].Path != "/a.go" {
		t.Errorf("first reclamation wrong: %+v", got[0])
	}
}

// An observe-mode would_block saved nothing — the read still happened.
// Counting it would credit the guard with uplift it did not deliver.
func TestReclamationsExcludesWouldBlock(t *testing.T) {
	dir := seedLedger(t,
		`{"ts":"2026-08-23T10:00:00Z","mode":"observe","session":"s","path":"/a.go","action":"would_block","est_tokens":5000,"repeat":true}`,
	)
	got, err := Reclamations(dir)
	if err != nil {
		t.Fatalf("reclamations: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("would_block must not count as reclaimed: %+v", got)
	}
}

// Each reclamation needs a stable identity so re-scanning the ledger does
// not double-count the same saving on every poll.
func TestReclamationIDIsStableAndDistinct(t *testing.T) {
	dir := seedLedger(t,
		`{"ts":"2026-08-23T10:00:00Z","mode":"active","session":"s","path":"/a.go","action":"blocked","est_tokens":100}`,
		`{"ts":"2026-08-23T10:00:01Z","mode":"active","session":"s","path":"/a.go","action":"blocked","est_tokens":100}`,
	)
	got, _ := Reclamations(dir)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].ID() == got[1].ID() {
		t.Error("two distinct blocks must not share an ID, or one is lost to dedup")
	}
	again, _ := Reclamations(dir)
	if again[0].ID() != got[0].ID() {
		t.Error("the same block must keep its ID across reads, or it is counted twice")
	}
}

// A missing ledger is an empty result, not an error — the daemon reads
// this before the guard has ever run.
func TestReclamationsMissingLedger(t *testing.T) {
	got, err := Reclamations(t.TempDir())
	if err != nil {
		t.Fatalf("missing ledger should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

// Malformed lines are skipped rather than failing the scan.
func TestReclamationsSkipsMalformedLines(t *testing.T) {
	dir := seedLedger(t,
		`not json`,
		`{"ts":"2026-08-23T10:00:00Z","mode":"active","session":"s","path":"/a.go","action":"blocked","est_tokens":100}`,
	)
	got, err := Reclamations(dir)
	if err != nil || len(got) != 1 {
		t.Errorf("got %+v err=%v, want the one good line", got, err)
	}
}

// Reclamations carry their timestamp so events land at the right time.
func TestReclamationCarriesTimestamp(t *testing.T) {
	dir := seedLedger(t,
		`{"ts":"2026-08-23T10:00:00Z","mode":"active","session":"s","path":"/a.go","action":"blocked","est_tokens":100}`,
	)
	got, _ := Reclamations(dir)
	want := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	if !got[0].At.Equal(want) {
		t.Errorf("At = %v, want %v", got[0].At, want)
	}
}
