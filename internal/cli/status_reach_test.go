package cli

import (
	"encoding/json"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
)

// dialFailure is the error chain net/http hands back when the dial fails
// with errno, so status is tested against real wrapping, not a string.
func dialFailure(errno error) error {
	return &url.Error{Op: "Get", URL: "http://127.0.0.1:7878/healthz", Err: &net.OpError{
		Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno),
	}}
}

type probeTimeout struct{}

func (probeTimeout) Error() string {
	return "context deadline exceeded (Client.Timeout exceeded while awaiting headers)"
}
func (probeTimeout) Timeout() bool   { return true }
func (probeTimeout) Temporary() bool { return true }

// isolateStatusHome keeps the supervisor evidence and config lookup off the
// developer's own machine.
func isolateStatusHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")
	t.Setenv("XDG_DATA_HOME", home+"/.local/share")
}

func runOfflineStatus(t *testing.T, netErr error, jsonOut bool) string {
	t.Helper()
	isolateStatusHome(t)
	prev := statusClient
	t.Cleanup(func() { SetStatusClient(prev) })
	SetStatusClient(&fakeDoer{netErr: netErr})
	args := []string{"status", "--addr", "127.0.0.1:7878"}
	if jsonOut {
		args = append(args, "--json")
	}
	out, err := executeRoot(t, args...)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	return out
}

// Each failure class is reported for what it establishes. Only a refused
// connection says the daemon is likely down; the rest leave it unknown.
func TestOfflineStatusClassifiesTheProbeFailure(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		class     string
		state     string
		health    string
		hintWants []string
	}{
		{"EPERM", dialFailure(syscall.EPERM), "permission_denied", "unknown", "unknown",
			[]string{"not permitted", "unrestricted shell", "loopback"}},
		{"ECONNREFUSED", dialFailure(syscall.ECONNREFUSED), "refused", "not_running", "likely_not_running",
			[]string{"daemon not running", "tokenops daemon install"}},
		{"timeout", &url.Error{Op: "Get", URL: "http://127.0.0.1:7878/healthz", Err: probeTimeout{}}, "timeout", "unknown", "unknown",
			[]string{"did not answer in time", "tokenops daemon status", "log"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got struct {
				Status       string `json:"status"`
				State        string `json:"state"`
				Ready        bool   `json:"ready"`
				DaemonHealth string `json:"daemon_health"`
				Hint         string `json:"hint"`
				Reachability struct {
					Reachable bool   `json:"reachable"`
					Class     string `json:"class"`
					Detail    string `json:"detail"`
					Summary   string `json:"summary"`
					Remedy    string `json:"remedy"`
				} `json:"reachability"`
				Supervisor *struct {
					UnitInstalled bool   `json:"unit_installed"`
					Evidence      string `json:"evidence"`
				} `json:"supervisor"`
			}
			out := runOfflineStatus(t, tc.err, true)
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("not json: %v\n%s", err, out)
			}
			// Kept for clients that branch on it.
			if got.Status != "daemon_unreachable" || got.Ready {
				t.Errorf("status/ready = %q/%v, want daemon_unreachable/false", got.Status, got.Ready)
			}
			if got.State != tc.state {
				t.Errorf("state = %q, want %q", got.State, tc.state)
			}
			if got.DaemonHealth != tc.health {
				t.Errorf("daemon_health = %q, want %q", got.DaemonHealth, tc.health)
			}
			if got.Reachability.Reachable || got.Reachability.Class != tc.class {
				t.Errorf("reachability = %+v, want class %q", got.Reachability, tc.class)
			}
			if got.Reachability.Summary == "" || got.Reachability.Remedy == "" || got.Reachability.Detail == "" {
				t.Errorf("reachability lacks summary/remedy/detail: %+v", got.Reachability)
			}
			if strings.Contains(got.Reachability.Detail, "http://") {
				t.Errorf("detail carries the URL: %q", got.Reachability.Detail)
			}
			for _, w := range tc.hintWants {
				if !strings.Contains(got.Hint, w) {
					t.Errorf("hint %q missing %q", got.Hint, w)
				}
			}
			if got.Supervisor == nil || got.Supervisor.UnitInstalled || !strings.Contains(got.Supervisor.Evidence, "unit file") {
				t.Errorf("supervisor evidence = %+v, want an uninstalled unit labelled by its source", got.Supervisor)
			}
		})
	}
}

// Inside a sandbox the daemon can be healthy while this client is denied
// the loopback dial. Restarting it is not the fix, and the text must not
// claim it is down.
func TestOfflineStatusPermissionDeniedDoesNotLeadWithRestart(t *testing.T) {
	out := runOfflineStatus(t, dialFailure(syscall.EPERM), false)
	if strings.Contains(out, "not running") {
		t.Errorf("EPERM reported as not running:\n%s", out)
	}
	for _, want := range []string{"permission_denied", "connect: operation not permitted", "unknown from this client", "unrestricted shell"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "hint:") && !strings.HasPrefix(trimmed, "blockers:") {
			continue
		}
		if r, u := strings.Index(trimmed, "restart"), strings.Index(trimmed, "unrestricted shell"); r >= 0 && r < u {
			t.Errorf("remedy leads with a restart: %q", trimmed)
		}
	}
}

func TestOfflineStatusRefusedKeepsTheOutageWording(t *testing.T) {
	out := runOfflineStatus(t, dialFailure(syscall.ECONNREFUSED), false)
	for _, want := range []string{"(not running)", "refused", "tokenops daemon install"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// A daemon that answers 503 everywhere is reachable: the statuses are the
// evidence, and it is not reported as unreachable.
func TestStatusKeepsHTTP503Evidence(t *testing.T) {
	isolateStatusHome(t)
	prev := statusClient
	t.Cleanup(func() { SetStatusClient(prev) })
	SetStatusClient(&fakeDoer{responses: map[string]fakeResponse{
		"/healthz": {status: 503, body: `{"status":"degraded"}`},
		"/readyz":  {status: 503, body: `{"status":"not_ready"}`},
		"/version": {status: 503, body: ``},
	}})
	out, err := executeRoot(t, "status", "--addr", "127.0.0.1:7878", "--json")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var got daemonProbe
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not json: %v\n%s", err, out)
	}
	if got.Health.Status != 503 || got.Ready.Status != 503 {
		t.Errorf("503 evidence lost: %+v / %+v", got.Health, got.Ready)
	}
	if got.Reachability == nil || !got.Reachability.Reachable {
		t.Errorf("reachability = %+v, want reachable", got.Reachability)
	}
	if strings.Contains(out, "daemon_unreachable") {
		t.Errorf("a daemon answering 503 reported unreachable: %s", out)
	}
}
