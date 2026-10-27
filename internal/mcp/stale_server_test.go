package mcp

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/version"
)

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

// The warning must name both binaries, so the operator can see which
// install the server is still holding and which one replaced it.
func TestDriftWarningNamesBothBinaries(t *testing.T) {
	d := BinaryDrift{OutOfDate: true, Running: "/old/tokenops", Installed: "/new/tokenops"}
	w := driftWarning(d)
	if !strings.Contains(w, d.Running) || !strings.Contains(w, d.Installed) {
		t.Errorf("warning must name both binaries: %s", w)
	}
	if driftWarning(BinaryDrift{}) != "" {
		t.Error("a current binary must not warn")
	}
}
