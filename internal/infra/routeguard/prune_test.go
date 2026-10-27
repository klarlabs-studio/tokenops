package routeguard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func touch(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Per-session state a session idle for a day no longer needs is removed,
// at most once an hour; anything else in the directory is left alone.
func TestPruneSessionsRemovesOnlyIdlePerSessionState(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-25 * time.Hour)
	fresh := now.Add(-time.Hour)
	staleSession := filepath.Join(dir, "session-old.json")
	staleNote := filepath.Join(dir, "headroom-old.json")
	freshSession := filepath.Join(dir, "session-new.json")
	freshNote := filepath.Join(dir, "headroom-new.json")
	longLived := filepath.Join(dir, "settings.json")
	otherKind := filepath.Join(dir, "session-old.jsonl")
	for _, p := range []string{staleSession, staleNote, longLived, otherKind} {
		touch(t, p, stale)
	}
	for _, p := range []string{freshSession, freshNote} {
		touch(t, p, fresh)
	}
	sub := filepath.Join(dir, "session-dir.json")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(sub, stale, stale)

	if err := PruneSessions(dir, now); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{staleSession, staleNote} {
		if exists(p) {
			t.Errorf("%s idle for a day was kept", filepath.Base(p))
		}
	}
	for _, p := range []string{freshSession, freshNote, longLived, otherKind, sub} {
		if !exists(p) {
			t.Errorf("%s was removed", filepath.Base(p))
		}
	}

	// Within the hour nothing is pruned again, even what is now stale.
	later := filepath.Join(dir, "session-later.json")
	touch(t, later, stale)
	if err := PruneSessions(dir, now.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !exists(later) {
		t.Error("pruned twice within the hour")
	}
	if err := PruneSessions(dir, now.Add(61*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if exists(later) {
		t.Error("an hour later the stale file was still kept")
	}
}

func TestPruneSessionsWithoutADirectoryCreatesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	if err := PruneSessions(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if exists(dir) {
		t.Error("pruning created the directory")
	}
}

// Writing a session's state or note prunes, so the directory stops
// growing without anyone running a command.
func TestWritingStatePrunes(t *testing.T) {
	dir := t.TempDir()
	stale := time.Now().Add(-48 * time.Hour)
	old := filepath.Join(dir, "session-gone.json")
	oldNote := filepath.Join(dir, "headroom-gone.json")
	touch(t, old, stale)
	touch(t, oldNote, stale)
	saveState(dir, "live", state{})
	if exists(old) {
		t.Error("saving a session's state did not prune")
	}
	_ = os.Remove(filepath.Join(dir, pruneStamp))
	if _, emit := DecideHeadroomNote(dir, "live", []NoteWindow{{Name: "5h", LeftPct: 40}}, time.Now(), time.UTC); !emit {
		t.Fatal("note not emitted")
	}
	if exists(oldNote) {
		t.Error("writing a headroom note did not prune")
	}
}
