package selfexe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// brewLayout lays out a Homebrew-style install under a temp dir: the binary
// lives in a versioned Caskroom directory and bin/tokenops is a symlink to
// it. Returns the symlink path (what the MCP client launched) and the
// versioned binary.
func brewLayout(t *testing.T, ver string) (dir, link, bin string) {
	t.Helper()
	dir = t.TempDir()
	bin = installVersion(t, dir, ver)
	link = filepath.Join(dir, "bin", "tokenops")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, link); err != nil {
		t.Fatal(err)
	}
	return dir, link, bin
}

func installVersion(t *testing.T, dir, ver string) string {
	t.Helper()
	bin := filepath.Join(dir, "Caskroom", "tokenops", ver, "tokenops")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("binary "+ver), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func relink(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func snapshotOf(t *testing.T, invoked string) *Snapshot {
	t.Helper()
	s, err := snapshot(invoked)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return s
}

func TestBinaryDriftQuietWhileInstallIsUnchanged(t *testing.T) {
	_, link, _ := brewLayout(t, "0.66.0")
	if d := snapshotOf(t, link).Check(); d.OutOfDate {
		t.Fatalf("fresh install reported out of date: %+v", d)
	}
}

// The incident: an agent's MCP server answered as 0.54.3 while 0.66.0 was
// installed. `brew upgrade` repoints bin/tokenops at the new Caskroom
// directory and deletes the old one, and the running server never noticed.
func TestBinaryDriftDetectsUpgradeThatRemovedTheRunningBinary(t *testing.T) {
	dir, link, old := brewLayout(t, "0.54.3")
	snap := snapshotOf(t, link)

	fresh := installVersion(t, dir, "0.66.0")
	relink(t, link, fresh)
	if err := os.RemoveAll(filepath.Dir(old)); err != nil {
		t.Fatal(err)
	}

	d := snap.Check()
	if !d.OutOfDate {
		t.Fatalf("upgrade not detected: %+v", d)
	}
	want, _ := filepath.EvalSymlinks(fresh)
	if d.Installed != want {
		t.Errorf("installed = %q, want %q", d.Installed, want)
	}
	if !strings.Contains(d.Running, "0.54.3") {
		t.Errorf("drift does not name the binary still running: %+v", d)
	}
}

// Homebrew keeps the previous version until cleanup runs, so the old binary
// can still exist while the link already points elsewhere.
func TestBinaryDriftDetectsRepointedLinkWhileOldBinaryRemains(t *testing.T) {
	dir, link, _ := brewLayout(t, "0.54.3")
	snap := snapshotOf(t, link)

	relink(t, link, installVersion(t, dir, "0.66.0"))

	if d := snap.Check(); !d.OutOfDate {
		t.Fatalf("repointed link not detected: %+v", d)
	}
}

// Without the link (uninstalled, or cleanup ran before the new version
// landed) the running binary's disappearance is the only evidence left.
func TestBinaryDriftDetectsRemovedBinary(t *testing.T) {
	_, link, old := brewLayout(t, "0.54.3")
	snap := snapshotOf(t, link)
	if err := os.RemoveAll(filepath.Dir(old)); err != nil {
		t.Fatal(err)
	}
	if d := snap.Check(); !d.OutOfDate {
		t.Fatalf("removed binary not detected: %+v", d)
	}
}

// `go install` and `make install` replace the file at the same path. The
// path still resolves to itself, so only the file's identity changes.
func TestBinaryDriftDetectsBinaryReplacedInPlace(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tokenops")
	if err := os.WriteFile(bin, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	snap := snapshotOf(t, bin)

	tmp := filepath.Join(dir, "tokenops.new")
	if err := os.WriteFile(tmp, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, bin); err != nil {
		t.Fatal(err)
	}
	if d := snap.Check(); !d.OutOfDate {
		t.Fatalf("in-place replacement not detected: %+v", d)
	}
}

func TestNilSnapshotReportsNoDrift(t *testing.T) {
	var s *Snapshot
	if d := s.Check(); d.OutOfDate {
		t.Fatalf("nil snapshot invented drift: %+v", d)
	}
}
