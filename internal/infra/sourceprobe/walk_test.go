package sourceprobe

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// settledClock is a clock far enough past every mtime the tests set that
// each directory counts as settled, so a warm pass trusts a directory
// whose modification time did not move and re-reads only the ones that
// did. A clock this late is the hard case: nothing is re-listed for
// being recent.
func settledClock() time.Time { return time.Now().Add(time.Hour) }

// The cached probe answers exactly what a full walk answers, through every
// kind of change a transcript tree sees between two probes. Each step
// changes the tree and then pins the touched directories' mtimes back to
// an old value, so a cache that trusted an unchanged directory mtime
// alone, without the identity and file checks, would fall behind.
func TestTreeCacheMatchesFullWalk(t *testing.T) {
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return old.Add(time.Duration(minutes) * time.Minute) }
	pinDir := func(t *testing.T, dir string) {
		t.Helper()
		if !hasChangeTime {
			t.Skip("pinning a directory's mtime back is invisible without a change time")
		}
		if err := os.Chtimes(dir, at(-1), at(-1)); err != nil {
			t.Fatal(err)
		}
	}
	steps := []struct {
		name string
		do   func(t *testing.T, root string)
	}{
		{"nothing changes", func(*testing.T, string) {}},
		{"append to an old transcript", func(t *testing.T, root string) {
			p := filepath.Join(root, "2026", "01", "02", "rollout-a.jsonl")
			f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString("{}\n"); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(p, at(50), at(50)); err != nil {
				t.Fatal(err)
			}
		}},
		{"new transcript in an existing day", func(t *testing.T, root string) {
			touch(t, filepath.Join(root, "2026", "01", "03", "rollout-c.jsonl"), at(60))
		}},
		{"new nested day and month directories", func(t *testing.T, root string) {
			touch(t, filepath.Join(root, "2026", "02", "01", "rollout-d.jsonl"), at(70))
		}},
		{"a compressed cold copy counts, other files do not", func(t *testing.T, root string) {
			touch(t, filepath.Join(root, "2026", "02", "01", "rollout-e.jsonl.zst"), at(80))
			touch(t, filepath.Join(root, "2026", "02", "01", "notes.txt"), at(500))
		}},
		{"delete the newest transcript", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "2026", "02", "01", "rollout-e.jsonl.zst")); err != nil {
				t.Fatal(err)
			}
		}},
		{"rename a transcript out of the tree's match", func(t *testing.T, root string) {
			day := filepath.Join(root, "2026", "02", "01")
			if err := os.Rename(filepath.Join(day, "rollout-d.jsonl"), filepath.Join(day, "rollout-d.bak")); err != nil {
				t.Fatal(err)
			}
		}},
		{"rename a transcript into another directory", func(t *testing.T, root string) {
			from := filepath.Join(root, "2026", "01", "03", "rollout-c.jsonl")
			to := filepath.Join(root, "2026", "02", "01", "rollout-c.jsonl")
			if err := os.Rename(from, to); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(to, at(90), at(90)); err != nil {
				t.Fatal(err)
			}
		}},
		{"delete a whole day directory", func(t *testing.T, root string) {
			if err := os.RemoveAll(filepath.Join(root, "2026", "02")); err != nil {
				t.Fatal(err)
			}
		}},
		{"replace a day directory with a different one of the same name, mtimes pinned", func(t *testing.T, root string) {
			day := filepath.Join(root, "2026", "01", "02")
			if err := os.RemoveAll(day); err != nil {
				t.Fatal(err)
			}
			touch(t, filepath.Join(day, "rollout-z.jsonl"), at(120))
			pinDir(t, day)
			pinDir(t, filepath.Join(root, "2026", "01"))
		}},
		{"new transcript with its directory's mtime pinned back", func(t *testing.T, root string) {
			day := filepath.Join(root, "2026", "01", "02")
			touch(t, filepath.Join(day, "rollout-y.jsonl"), at(130))
			pinDir(t, day)
		}},
		{"empty the tree", func(t *testing.T, root string) {
			if err := os.RemoveAll(filepath.Join(root, "2026")); err != nil {
				t.Fatal(err)
			}
		}},
	}

	root := t.TempDir()
	touch(t, filepath.Join(root, "2026", "01", "02", "rollout-a.jsonl"), at(10))
	touch(t, filepath.Join(root, "2026", "01", "02", "rollout-b.jsonl"), at(20))
	for _, d := range []string{
		filepath.Join(root, "2026", "01", "02"),
		filepath.Join(root, "2026", "01"),
		filepath.Join(root, "2026"),
	} {
		pinDir(t, d)
	}

	cache := newTreeCache(24*time.Hour, settledClock)
	if got, want := probeBoth(t, cache, root); got != want {
		t.Fatalf("cold: cached %v, full walk %v", got, want)
	}
	for _, step := range steps {
		step.do(t, root)
		got, want := probeBoth(t, cache, root)
		if got != want {
			t.Fatalf("%s: cached %v, full walk %v", step.name, got, want)
		}
	}
}

type probeAnswer struct {
	at    time.Time
	known bool
}

func probeBoth(t *testing.T, cache *treeCache, root string) (cached, full probeAnswer) {
	t.Helper()
	at, ok := cache.newest(root, isTranscript)
	cached = probeAnswer{at, ok}
	at, ok = walkNewest(root, isTranscript)
	full = probeAnswer{at, ok}
	return cached, full
}

// A warm probe of an unchanged tree reads no directory: it re-checks
// each directory's identity and each transcript's mtime, and nothing
// else.
func TestTreeCacheWarmProbeListsNoDirectory(t *testing.T) {
	root := t.TempDir()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for d := range 5 {
		dir := filepath.Join(root, "2026", "01", fmt.Sprintf("%02d", d+1))
		touch(t, filepath.Join(dir, "rollout.jsonl"), old)
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}
	cache := newTreeCache(24*time.Hour, settledClock)
	cache.newest(root, isTranscript)
	cold := cache.listings
	cache.newest(root, isTranscript)
	if warm := cache.listings - cold; warm != 0 {
		t.Fatalf("warm probe listed %d directories, want 0", warm)
	}
}

// A directory whose mtime is within the settle window of the probe is
// listed again on the next probe, however unchanged it looks: on a file
// system with coarse timestamps, an entry created in the same tick as
// the listing leaves the mtime as it was.
func TestTreeCacheRelistsAnUnsettledDirectory(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(root, now, now); err != nil {
		t.Fatal(err)
	}
	cache := newTreeCache(24*time.Hour, func() time.Time { return now })
	cache.newest(root, isTranscript)
	before := cache.listings
	cache.newest(root, isTranscript)
	if cache.listings == before {
		t.Fatal("an unsettled directory was trusted")
	}
}

// The slow full re-walk runs once its interval passed, whatever the
// directories' mtimes say.
func TestTreeCacheRewalksFullyOnSchedule(t *testing.T) {
	root := t.TempDir()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dir := filepath.Join(root, "a")
	touch(t, filepath.Join(dir, "x.jsonl"), old)
	for _, d := range []string{dir, root} {
		if err := os.Chtimes(d, old, old); err != nil {
			t.Fatal(err)
		}
	}
	now := settledClock()
	cache := newTreeCache(10*time.Minute, func() time.Time { return now })
	cache.newest(root, isTranscript)
	start := cache.listings
	now = now.Add(9 * time.Minute)
	cache.newest(root, isTranscript)
	if cache.listings != start {
		t.Fatalf("listed %d directories before the full re-walk was due", cache.listings-start)
	}
	now = now.Add(time.Minute)
	cache.newest(root, isTranscript)
	if got := cache.listings - start; got != 2 {
		t.Fatalf("full re-walk listed %d directories, want 2", got)
	}
}

// buildTree lays out a Codex-shaped tree: sessions/YYYY/MM/DD with a few
// rollouts a day, every mtime in the past so the warm probe can trust
// what it saw.
func buildTree(b *testing.B, days, perDay int) string {
	b.Helper()
	root := b.TempDir()
	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for d := range days {
		day := old.AddDate(0, 0, d)
		dir := filepath.Join(root, day.Format("2006"), day.Format("01"), day.Format("02"))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			b.Fatal(err)
		}
		for i := range perDay {
			p := filepath.Join(dir, fmt.Sprintf("rollout-%d.jsonl", i))
			if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
				b.Fatal(err)
			}
			if err := os.Chtimes(p, day, day); err != nil {
				b.Fatal(err)
			}
		}
	}
	return root
}

// 600 days of five rollouts: 3,000 transcripts in about 620 directories.
func BenchmarkNewestTranscript(b *testing.B) {
	root := buildTree(b, 600, 5)
	b.Run("full walk", func(b *testing.B) {
		for b.Loop() {
			walkNewest(root, isTranscript)
		}
	})
	b.Run("warm cache", func(b *testing.B) {
		cache := newTreeCache(24*time.Hour, settledClock)
		cache.newest(root, isTranscript)
		for b.Loop() {
			cache.newest(root, isTranscript)
		}
	})
}
