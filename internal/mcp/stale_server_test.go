package mcp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/version"
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

func snapshotOf(t *testing.T, invoked string) *ExecutableSnapshot {
	t.Helper()
	s, err := snapshotExecutable(invoked)
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
	if w := d.Warning(); !strings.Contains(w, d.Running) || !strings.Contains(w, d.Installed) {
		t.Errorf("warning must name both binaries: %s", w)
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
	var s *ExecutableSnapshot
	if d := s.Check(); d.OutOfDate {
		t.Fatalf("nil snapshot invented drift: %+v", d)
	}
}

func readyConfigured() ControlDeps {
	return ControlDeps{ReadyCheck: func() bool { return true }}
}

func outOfDate() BinaryDrift {
	return BinaryDrift{
		OutOfDate: true,
		Running:   "/opt/homebrew/Caskroom/tokenops/0.54.3/tokenops",
		Installed: "/opt/homebrew/Caskroom/tokenops/0.66.0/tokenops",
	}
}

// A server that is out of date answers every question with old code. The
// operator has to hear it from the one tool that reports on tokenops itself,
// and has to be told which program to restart: the client owns the server.
func TestStatusWarnsWhenThisMCPServerIsOutOfDate(t *testing.T) {
	d := readyConfigured()
	d.BinaryDrift = outOfDate
	res := statusInfo(d)

	if !res.ServerOutOfDate {
		t.Errorf("server_out_of_date not set: %+v", res)
	}
	if res.State != "degraded" {
		t.Errorf("state = %q, want degraded", res.State)
	}
	if !res.Ready {
		t.Errorf("an out-of-date server still answers; ready must stay true")
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "out of date") {
		t.Fatalf("want the out-of-date warning first, got %#v", res.Warnings)
	}
	if !strings.Contains(res.Warnings[0], version.String()) {
		t.Errorf("warning does not name the running version %q: %s", version.String(), res.Warnings[0])
	}
	if !slices.Contains(res.NextActions, StaleServerNextAction) {
		t.Errorf("want the restart-your-client remediation, got %#v", res.NextActions)
	}
	// The vendor-usage remediation is for silent sources; it has nothing
	// to do with a stale server and would send the operator the wrong way.
	for _, a := range res.NextActions {
		if strings.Contains(a, "vendor-usage status") {
			t.Errorf("unrelated ingestion remediation added: %#v", res.NextActions)
		}
	}
}

func TestStatusSilentWhenThisMCPServerIsCurrent(t *testing.T) {
	d := readyConfigured()
	d.BinaryDrift = func() BinaryDrift { return BinaryDrift{Running: "/usr/local/bin/tokenops"} }
	res := statusInfo(d)
	if res.ServerOutOfDate || len(res.Warnings) != 0 || res.State != "ready" {
		t.Fatalf("current server reported as stale: %+v", res)
	}
}

func TestStatusReportsDaemonVersion(t *testing.T) {
	var asked string
	d := readyConfigured()
	d.DaemonProbe = func() DaemonReport { return DaemonReport{URL: "http://127.0.0.1:9", Alive: true} }
	d.DaemonVersion = func(url string) string { asked = url; return "0.66.0" }
	res := statusInfo(d)
	if res.DaemonVersion != "0.66.0" {
		t.Errorf("daemon_version = %q, want 0.66.0", res.DaemonVersion)
	}
	if asked != "http://127.0.0.1:9" {
		t.Errorf("version fetched from %q, want the probed daemon's URL", asked)
	}
}

// No daemon, no version: asking an address nothing answers on would only
// add a timeout to every status call.
func TestStatusSkipsDaemonVersionWhenNoDaemonAnswers(t *testing.T) {
	d := readyConfigured()
	d.DaemonProbe = func() DaemonReport { return DaemonReport{} }
	d.DaemonVersion = func(string) string { t.Fatal("version fetched from an unreachable daemon"); return "" }
	if res := statusInfo(d); res.DaemonVersion != "" {
		t.Errorf("daemon_version = %q with no daemon", res.DaemonVersion)
	}
}

func TestVersionToolReportsServerDriftAndDaemonVersion(t *testing.T) {
	d := ControlDeps{
		BinaryDrift:   outOfDate,
		DaemonProbe:   func() DaemonReport { return DaemonReport{URL: "http://127.0.0.1:9", Alive: true} },
		DaemonVersion: func(string) string { return "0.66.0" },
	}
	res := versionInfo(d)
	if res.Version != version.Version {
		t.Errorf("version = %q, want this server's %q", res.Version, version.Version)
	}
	if res.DaemonVersion != "0.66.0" {
		t.Errorf("daemon_version = %q, want 0.66.0", res.DaemonVersion)
	}
	if !res.ServerOutOfDate || len(res.Warnings) == 0 || !slices.Contains(res.NextActions, StaleServerNextAction) {
		t.Errorf("version tool hides the stale server: %+v", res)
	}
}

func TestVersionToolQuietWithoutHooks(t *testing.T) {
	res := versionInfo(ControlDeps{})
	if res.ServerOutOfDate || len(res.Warnings) != 0 || res.DaemonVersion != "" {
		t.Errorf("zero deps invented findings: %+v", res)
	}
}

func TestFetchDaemonVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"version":"0.66.0","commit":"abc","date":"2026-09-01"}`))
	}))
	defer srv.Close()
	if got := FetchDaemonVersion(srv.URL); got != "0.66.0" {
		t.Errorf("FetchDaemonVersion = %q, want 0.66.0", got)
	}
}

// An unknown version stays unknown; it is never guessed.
func TestFetchDaemonVersionUnknownOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if got := FetchDaemonVersion(srv.URL); got != "" {
		t.Errorf("FetchDaemonVersion on 404 = %q, want empty", got)
	}
	if got := FetchDaemonVersion(""); got != "" {
		t.Errorf("FetchDaemonVersion with no URL = %q, want empty", got)
	}
}
