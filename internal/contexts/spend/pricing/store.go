package pricing

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// snapshotsSubdir is the directory (under the pricing root) holding the
// append-only timestamped snapshot files.
const snapshotsSubdir = "snapshots"

// ResolveDir returns the pricing state root, defaulting to
// ~/.tokenops/pricing when dir is empty. On a machine with no resolvable
// home it falls back to a relative dir so the command still works (writes
// land next to the CWD rather than erroring).
func ResolveDir(dir string) string {
	if dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".tokenops-pricing"
	}
	return filepath.Join(home, ".tokenops", "pricing")
}

// snapshotsDir is the snapshots subdirectory under the resolved root.
func snapshotsDir(dir string) string {
	return filepath.Join(ResolveDir(dir), snapshotsSubdir)
}

// snapshotFilename derives a filesystem-safe filename from a snapshot's
// FetchedAt. RFC3339 contains ':' which is legal on POSIX but reserved on
// Windows and awkward in shells, so colons are replaced with '-'. The result
// stays lexically sortable because RFC3339 is fixed-width and big-endian.
func snapshotFilename(t time.Time) string {
	stamp := t.UTC().Format(time.RFC3339)
	safe := strings.NewReplacer(":", "-", "/", "-", "\\", "-").Replace(stamp)
	return safe + ".json"
}

// SaveSnapshot writes s to dir/snapshots/<RFC3339>.json atomically (temp +
// rename), creating the directory tree as needed. The baseline snapshot is
// never persisted — it is always available in memory via BaselineSnapshot —
// so saving one is a no-op.
func SaveSnapshot(dir string, s Snapshot) (string, error) {
	if s.Source == SourceEmbeddedBaseline {
		return "", nil
	}
	sdir := snapshotsDir(dir)
	if err := os.MkdirAll(sdir, 0o755); err != nil {
		return "", fmt.Errorf("pricing: create snapshots dir: %w", err)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", fmt.Errorf("pricing: marshal snapshot: %w", err)
	}
	final := filepath.Join(sdir, snapshotFilename(s.FetchedAt))
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return "", fmt.Errorf("pricing: write snapshot: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("pricing: commit snapshot: %w", err)
	}
	return final, nil
}

// LoadSnapshots reads every persisted snapshot under dir, sorted ascending by
// FetchedAt (baseline is NOT included — call BaselineSnapshot for that). A
// missing directory yields an empty slice, not an error: an operator who has
// never refreshed simply has no snapshots yet. Individual unreadable or
// malformed files are skipped (fail-soft) rather than failing the whole load.
func LoadSnapshots(dir string) []Snapshot {
	sdir := snapshotsDir(dir)
	entries, err := os.ReadDir(sdir)
	if err != nil {
		return nil
	}
	var out []Snapshot
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(sdir, e.Name()))
		if err != nil {
			continue
		}
		var s Snapshot
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		if s.Rates == nil {
			s.Rates = map[string]Rate{}
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].FetchedAt.Before(out[j].FetchedAt)
	})
	return out
}

// LatestSnapshot returns the most recent persisted snapshot and true, or the
// embedded baseline and false when none has been saved yet. Callers that want
// "newest available rate card" use this directly; the bool distinguishes a
// real refresh from the fallback.
func LatestSnapshot(dir string) (Snapshot, bool) {
	snaps := LoadSnapshots(dir)
	if len(snaps) == 0 {
		return BaselineSnapshot(), false
	}
	return snaps[len(snaps)-1], true
}

// FindSnapshot resolves a selector to a snapshot. Recognised selectors:
//   - "" or "latest": the newest persisted snapshot, else baseline
//   - "baseline":      the embedded baseline
//   - an RFC3339 timestamp or filename stem: the snapshot whose FetchedAt
//     formats to that stamp (prefix-matched, so a date like "2026-07-08"
//     selects the first snapshot on that day)
//
// It returns ErrSnapshotNotFound when a specific selector matches nothing.
func FindSnapshot(dir, selector string) (Snapshot, error) {
	switch strings.ToLower(strings.TrimSpace(selector)) {
	case "", "latest":
		s, _ := LatestSnapshot(dir)
		return s, nil
	case "baseline":
		return BaselineSnapshot(), nil
	}
	want := strings.TrimSuffix(selector, ".json")
	for _, s := range LoadSnapshots(dir) {
		stamp := strings.TrimSuffix(snapshotFilename(s.FetchedAt), ".json")
		if stamp == want || strings.HasPrefix(stamp, want) {
			return s, nil
		}
	}
	return Snapshot{}, fmt.Errorf("%w: %q", ErrSnapshotNotFound, selector)
}
