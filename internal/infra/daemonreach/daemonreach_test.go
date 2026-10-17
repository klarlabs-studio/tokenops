package daemonreach

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
)

// dialErr builds the error chain net/http returns when a dial fails, so the
// classifier is exercised against the real wrapping rather than a string.
func dialErr(errno error) error {
	return &url.Error{Op: "Get", URL: "http://127.0.0.1:7878/healthz", Err: &net.OpError{
		Op: "dial", Net: "tcp",
		Err: os.NewSyscallError("connect", errno),
	}}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Class
	}{
		{"nil", nil, Reachable},
		{"EPERM", dialErr(syscall.EPERM), PermissionDenied},
		{"EACCES", dialErr(syscall.EACCES), PermissionDenied},
		{"ECONNREFUSED", dialErr(syscall.ECONNREFUSED), Refused},
		{"client timeout", &url.Error{Op: "Get", URL: "http://x/healthz", Err: timeoutErr{}}, Timeout},
		{"context deadline", fmt.Errorf("probe: %w", context.DeadlineExceeded), Timeout},
		{"dns", &url.Error{Op: "Get", URL: "http://nope/healthz", Err: &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "nope"}}}, Discovery},
		{"no host", &url.Error{Op: "Get", URL: "http:///healthz", Err: errors.New("http: no Host in request URL")}, Discovery},
		{"bad scheme", &url.Error{Op: "Get", URL: "ftp://x", Err: errors.New(`unsupported protocol scheme "ftp"`)}, Discovery},
		{"bad port", &url.Error{Op: "Get", URL: "http://x:y", Err: &net.AddrError{Err: "invalid port", Addr: "x:y"}}, Discovery},
		{"string refused", errors.New("connection refused"), Refused},
		{"string eperm", errors.New("dial tcp 127.0.0.1:7878: connect: operation not permitted"), PermissionDenied},
		{"other", errors.New("something odd"), Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.err); got != tc.want {
				t.Errorf("Classify(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// Detail names the failing call and errno without the request URL, which
// can carry whatever an operator put in --addr.
func TestDetailIsTheSyscallCause(t *testing.T) {
	if got := Detail(dialErr(syscall.EPERM)); got != "connect: operation not permitted" {
		t.Errorf("Detail = %q", got)
	}
	if d := Detail(&url.Error{Op: "Get", URL: "http://secret@x/healthz", Err: timeoutErr{}}); strings.Contains(d, "secret") {
		t.Errorf("Detail leaked the URL: %q", d)
	}
	if d := Detail(&url.Error{Op: "Get", URL: "http://secret@x/healthz", Err: errors.New("odd")}); strings.Contains(d, "secret") {
		t.Errorf("Detail leaked the URL: %q", d)
	}
}

// Only a refused connection says the daemon is probably down. Every other
// failure is a fact about this client's view.
func TestOnlyRefusedImpliesDown(t *testing.T) {
	for _, c := range []Class{PermissionDenied, Timeout, Discovery, HTTPStatus, Unknown, Reachable} {
		if c.ImpliesDown() {
			t.Errorf("%s implies the daemon is down", c)
		}
	}
	if !Refused.ImpliesDown() {
		t.Error("refused does not imply the daemon is down")
	}
}

// The remedy for a permission denial must lead with checking the client's
// access, not with restarting a daemon that may be healthy.
func TestRemedyPerClass(t *testing.T) {
	const restart = "run `tokenops daemon restart`"
	cases := map[Class][]string{
		PermissionDenied: {"unrestricted shell", "loopback"},
		Refused:          {restart},
		Timeout:          {"tokenops daemon status", "log"},
		Discovery:        {"--addr", "listen"},
		HTTPStatus:       {"log"},
		Unknown:          {"unrestricted shell", restart},
	}
	for c, wants := range cases {
		got := c.Remedy(restart)
		for _, w := range wants {
			if !strings.Contains(got, w) {
				t.Errorf("%s remedy %q missing %q", c, got, w)
			}
		}
	}
	perm := PermissionDenied.Remedy(restart)
	if strings.Index(perm, "restart") < strings.Index(perm, "unrestricted shell") {
		t.Errorf("permission remedy leads with a restart: %q", perm)
	}
	if !strings.HasPrefix(Refused.Remedy(restart), restart) {
		t.Errorf("refused remedy should lead with the restart: %q", Refused.Remedy(restart))
	}
}

func TestSummaryNamesTheClass(t *testing.T) {
	wants := map[Class]string{
		PermissionDenied: "not permitted",
		Refused:          "refused",
		Timeout:          "did not answer in time",
		Discovery:        "address",
		Unknown:          "could not reach",
	}
	for c, w := range wants {
		if got := c.Summary(); !strings.Contains(got, w) {
			t.Errorf("%s summary %q missing %q", c, got, w)
		}
	}
	if s := PermissionDenied.Summary(); !strings.Contains(s, "may be running") {
		t.Errorf("permission summary should leave the daemon's state open: %q", s)
	}
}
