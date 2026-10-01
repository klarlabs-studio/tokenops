package routehistory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const fw = "https://api.fireworks.ai/inference"

func TestObserveRecordsChangesAndPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "route-history.jsonl")
	tr, err := OpenAt(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if wrote, _ := tr.Observe(HarnessClaudeCode, "", now); !wrote {
		t.Fatal("first sighting not recorded")
	}
	if wrote, _ := tr.Observe(HarnessClaudeCode, "", now.Add(time.Minute)); wrote {
		t.Error("an unchanged route was recorded again")
	}
	if wrote, _ := tr.Observe(HarnessClaudeCode, fw, now.Add(time.Hour)); !wrote {
		t.Fatal("a switch was not recorded")
	}
	again, err := OpenAt(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.At(HarnessClaudeCode, now.Add(2*time.Hour)); got != fw {
		t.Errorf("after reload At = %q", got)
	}
	if got := again.At(HarnessClaudeCode, now.Add(30*time.Minute)); got != "" {
		t.Errorf("before the switch At = %q", got)
	}
}

// FireConnect's backup of the previous settings dates the switch, so
// turns from before TokenOps first looked are attributed correctly.
func TestFirstSightingIsDatedByFireConnectsBackup(t *testing.T) {
	home := t.TempDir()
	backup := filepath.Join(home, ".fireconnect", "claude", "provider-backup.json")
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	switched := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	if err := os.Chtimes(backup, switched, switched); err != nil {
		t.Fatal(err)
	}
	tr, err := OpenAt(filepath.Join(home, ".tokenops", "route-history.jsonl"), home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Observe(HarnessClaudeCode, fw, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if got := tr.At(HarnessClaudeCode, switched.Add(-time.Hour)); got != "" {
		t.Errorf("before FireConnect: %q, want Anthropic's default", got)
	}
	if got := tr.At(HarnessClaudeCode, switched.Add(time.Hour)); got != fw {
		t.Errorf("after FireConnect: %q", got)
	}
}
