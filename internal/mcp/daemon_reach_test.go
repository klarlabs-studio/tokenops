package mcp

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/daemonreach"
)

// roundTripFunc fakes the probe's transport so no daemon or socket is
// involved.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fakeTimeout struct{}

func (fakeTimeout) Error() string   { return "i/o timeout" }
func (fakeTimeout) Timeout() bool   { return true }
func (fakeTimeout) Temporary() bool { return true }

func dialError(errno error) error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno)}
}

func withProbeTransport(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir()) // no URL hint
	prev := probeTransport
	probeTransport = rt
	t.Cleanup(func() { probeTransport = prev })
}

func failingWith(err error) http.RoundTripper {
	return roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, err })
}

func answering(status int) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"status":"degraded"}`)), Request: r}, nil
	})
}

func TestProbeClassifiesEachFailure(t *testing.T) {
	cases := []struct {
		name   string
		rt     http.RoundTripper
		class  daemonreach.Class
		status int
	}{
		{"EPERM", failingWith(dialError(syscall.EPERM)), daemonreach.PermissionDenied, 0},
		{"ECONNREFUSED", failingWith(dialError(syscall.ECONNREFUSED)), daemonreach.Refused, 0},
		{"timeout", failingWith(fakeTimeout{}), daemonreach.Timeout, 0},
		{"HTTP 503", answering(http.StatusServiceUnavailable), daemonreach.HTTPStatus, 503},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withProbeTransport(t, tc.rt)
			r := probeDaemonAt("http://127.0.0.1:7878")
			if r.Alive {
				t.Fatalf("alive on a failed probe: %+v", r)
			}
			if r.Class != tc.class || r.HTTPStatus != tc.status {
				t.Errorf("report = %+v, want class %q status %d", r, tc.class, tc.status)
			}
		})
	}
}

func TestProbeAnsweringIsReachable(t *testing.T) {
	withProbeTransport(t, answering(http.StatusOK))
	if r := probeDaemonAt("http://127.0.0.1:7878"); !r.Alive || r.Class != daemonreach.Reachable {
		t.Errorf("report = %+v, want alive and reachable", r)
	}
}

// With no hint and no configured address there is nothing to ask; that is
// a discovery failure, not an absent daemon.
func TestProbeWithNoAddressIsADiscoveryFailure(t *testing.T) {
	withProbeTransport(t, failingWith(errors.New("must not be called")))
	if r := probeDaemonAt(""); r.Class != daemonreach.Discovery {
		t.Errorf("class = %q, want discovery", r.Class)
	}
}

// The serve-side warning may assert that nothing is writing only when the
// connection was refused. From any other failure it can say only that it
// could not reach the daemon, and why.
func TestDaemonPresenceWarningPerClass(t *testing.T) {
	cases := []struct {
		report   DaemonReport
		wants    []string
		mustNot  []string
		leadsNot string
	}{
		{DaemonReport{Class: daemonreach.Refused}, []string{"nothing is writing to the event store", "refused", "tokenops daemon restart"}, nil, ""},
		{DaemonReport{Class: daemonreach.PermissionDenied, Detail: "connect: operation not permitted"},
			[]string{"could not reach", "permission_denied", "operation not permitted", "unrestricted shell"},
			[]string{"nothing is writing"}, "restart"},
		{DaemonReport{Class: daemonreach.Timeout}, []string{"could not reach", "timeout", "tokenops daemon status"}, []string{"nothing is writing"}, ""},
		{DaemonReport{Class: daemonreach.Discovery}, []string{"could not reach", "discovery", "listen"}, []string{"nothing is writing"}, ""},
		{DaemonReport{URL: "http://127.0.0.1:7878", Class: daemonreach.HTTPStatus, HTTPStatus: 503}, []string{"HTTP 503", "running but not healthy"}, []string{"nothing is writing"}, ""},
	}
	for _, tc := range cases {
		t.Run(string(tc.report.Class), func(t *testing.T) {
			warn := daemonPresenceWarning(tc.report, true)
			for _, w := range tc.wants {
				if !strings.Contains(warn, w) {
					t.Errorf("warning missing %q: %s", w, warn)
				}
			}
			for _, bad := range tc.mustNot {
				if strings.Contains(warn, bad) {
					t.Errorf("warning asserts %q from %s: %s", bad, tc.report.Class, warn)
				}
			}
			next := daemonPresenceNextAction(tc.report)
			if tc.leadsNot != "" && strings.HasPrefix(next, "bring the ingestion daemon back") {
				t.Errorf("next action for %s leads with a restart: %s", tc.report.Class, next)
			}
		})
	}
}

// tokenops_status carries the class as data next to the warning, so an
// agent can branch on it without parsing prose; stale-reader warnings stay
// alongside, independent of it.
func TestStatusReportsDaemonReachability(t *testing.T) {
	res := statusInfo(ControlDeps{
		ReadyCheck: func() bool { return true },
		DaemonProbe: func() DaemonReport {
			return DaemonReport{Class: daemonreach.PermissionDenied, Detail: "connect: operation not permitted"}
		},
		StaleSources: func() []config.StaleSource {
			return []config.StaleSource{{Name: "opencode", SourceTag: "opencode", WindowHours: 48, SilentFor: 72 * time.Hour}}
		},
	})
	if res.DaemonReachability == nil || res.DaemonReachability.Class != daemonreach.PermissionDenied || res.DaemonReachability.Reachable {
		t.Fatalf("daemon_reachability = %+v", res.DaemonReachability)
	}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"daemon_reachability":{"reachable":false,"class":"permission_denied"`) {
		t.Errorf("json lacks daemon_reachability: %s", data)
	}
	joined := strings.Join(res.Warnings, "\n")
	if strings.Contains(joined, "nothing is writing") {
		t.Errorf("EPERM asserted an ingestion outage: %s", joined)
	}
	if !strings.Contains(joined, "ingestion stale") || !strings.Contains(joined, "opencode") {
		t.Errorf("stale-reader warning lost: %s", joined)
	}
	for _, a := range res.NextActions {
		if a == DaemonPresenceNextAction {
			t.Errorf("EPERM got the restart next action: %v", res.NextActions)
		}
	}
}
