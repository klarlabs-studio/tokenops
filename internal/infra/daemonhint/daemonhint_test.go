package daemonhint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeHint(t *testing.T, p Payload) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "tokenops"), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokenops", "daemon.url"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPathFollowsXDGDataHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg")
	got, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := filepath.Join("/tmp/xdg", "tokenops", "daemon.url"); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

func TestPathFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := filepath.Join(home, ".tokenops", "daemon.url"); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

func TestReadRoundTrips(t *testing.T) {
	started := time.Now().UTC().Truncate(time.Second)
	writeHint(t, Payload{
		URL:            "http://127.0.0.1:7878",
		LocalURL:       "http://tokenops.local:7878",
		Addr:           "127.0.0.1:7878",
		PID:            4242,
		StartedAt:      started,
		DashboardToken: "tok",
	})

	got, err := Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.URL != "http://127.0.0.1:7878" || got.PID != 4242 || got.DashboardToken != "tok" {
		t.Errorf("Read() = %+v", got)
	}
	if !got.StartedAt.Equal(started) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, started)
	}
}

// A missing hint must be distinguishable from a corrupt one: callers branch
// on "no daemon is running" and would otherwise report a parse error as a
// stopped daemon.
func TestReadReportsMissingAsNotExist(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if _, err := Read(); !os.IsNotExist(err) {
		t.Fatalf("Read() error = %v, want os.IsNotExist", err)
	}
}

func TestPreferredURLPrefersMDNS(t *testing.T) {
	p := Payload{URL: "http://127.0.0.1:7878", LocalURL: "http://tokenops.local:7878"}
	if got := p.PreferredURL(); got != "http://tokenops.local:7878" {
		t.Errorf("PreferredURL() = %q, want the mDNS URL", got)
	}
	p.LocalURL = ""
	if got := p.PreferredURL(); got != "http://127.0.0.1:7878" {
		t.Errorf("PreferredURL() without mDNS = %q, want the loopback URL", got)
	}
}

func TestTokenReadsTheHint(t *testing.T) {
	writeHint(t, Payload{URL: "http://127.0.0.1:7878", DashboardToken: "s3cret"})
	if got := Token(); got != "s3cret" {
		t.Errorf("Token() = %q, want the hint's token", got)
	}
}

// Token is best-effort on purpose: a caller with no token should send the
// request bare and let the daemon answer 401, which diagnoses the problem
// better than refusing to ask.
func TestTokenIsEmptyWithoutAHint(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if got := Token(); got != "" {
		t.Errorf("Token() = %q, want empty", got)
	}
}

func TestTokenIsEmptyWhenHintIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "tokenops"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokenops", "daemon.url"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Token(); got != "" {
		t.Errorf("Token() = %q, want empty", got)
	}
}
