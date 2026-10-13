package agentdx

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// benchCorpus writes a week of synthetic transcripts the size of a busy
// machine's: 300 Claude Code sessions and 100 Codex rollouts, plus as
// many older files last written before the window.
func benchCorpus(b *testing.B) (cc, cx string, active []string) {
	b.Helper()
	dir := b.TempDir()
	cc, cx = filepath.Join(dir, "claude"), filepath.Join(dir, "codex")
	write := func(path, body string, stamp time.Time) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			b.Fatal(err)
		}
		if !stamp.IsZero() {
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				b.Fatal(err)
			}
		}
	}
	old := cacheEpoch.Add(-30 * 24 * time.Hour)
	for i := range 300 {
		p := filepath.Join(cc, fmt.Sprintf("proj-%d", i%20), fmt.Sprintf("s%d.jsonl", i))
		write(p, turns(cacheEpoch.Add(time.Duration(i)*time.Minute), 120, i*1000, claudeTurn), time.Time{})
		if i%30 == 0 {
			active = append(active, p)
		}
		write(filepath.Join(cc, fmt.Sprintf("proj-%d", i%20), fmt.Sprintf("old%d.jsonl", i)), turns(old, 120, i, claudeTurn), old)
	}
	for i := range 100 {
		day := cacheEpoch.Add(time.Duration(i%7) * 24 * time.Hour)
		p := filepath.Join(cx, day.Format("2006/01/02"), fmt.Sprintf("rollout-%s-%04d.jsonl", day.Format("2006-01-02T15-04-05"), i))
		write(p, turns(day, 80, i*1000, codexTurn), time.Time{})
	}
	return cc, cx, active
}

func scanWeek(b *testing.B, cc, cx string, cache *TranscriptCache) int {
	since := cacheEpoch.Add(-time.Hour)
	a, err := Extract(ExtractOptions{Root: cc, Since: since, IncludeScratch: true, Cache: cache})
	if err != nil {
		b.Fatal(err)
	}
	c, err := ExtractCodex(ExtractOptions{Root: cx, Since: since, Cache: cache})
	if err != nil {
		b.Fatal(err)
	}
	return len(a) + len(c)
}

// BenchmarkScanWeek compares a full read of the week with the daemon's
// next scan, by which a tenth of the Claude Code sessions have grown.
func BenchmarkScanWeek(b *testing.B) {
	cc, cx, active := benchCorpus(b)
	b.Run("full", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			scanWeek(b, cc, cx, nil)
		}
	})
	b.Run("rescan", func(b *testing.B) {
		cache := NewTranscriptCache()
		scanWeek(b, cc, cx, cache)
		n := 0
		b.ReportAllocs()
		for b.Loop() {
			b.StopTimer()
			for _, p := range active {
				f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // bench temp dir
				if err != nil {
					b.Fatal(err)
				}
				_, _ = f.WriteString(claudeTurn(cacheEpoch.Add(200*time.Hour), n))
				_ = f.Close()
			}
			n++
			b.StartTimer()
			scanWeek(b, cc, cx, cache)
		}
	})
}
