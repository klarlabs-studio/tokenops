// Package daemonreach classifies why a client could not reach the ingestion
// daemon.
//
// "This client could not connect" and "the daemon is not running" are
// different facts. Inside a sandboxed coding agent the loopback dial fails
// with EPERM while the daemon is healthy, and status surfaces that read every
// failed probe as an outage told the operator to restart a daemon that was
// fine. Only a refused connection is evidence that nothing listens at the
// address; every other failure says something about this client's view, and
// leaves the daemon's own state unknown.
package daemonreach

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
)

// Class is why a probe of the daemon did or did not get an answer.
type Class string

// Classes, from "it answered" to "we cannot say why not".
const (
	// Reachable: an HTTP answer arrived.
	Reachable Class = "reachable"
	// HTTPStatus: the daemon answered, with a non-2xx status.
	HTTPStatus Class = "http_status"
	// PermissionDenied: the OS refused this client the connection
	// (EPERM/EACCES) — a sandbox or network policy, not the daemon.
	PermissionDenied Class = "permission_denied"
	// Refused: nothing is listening at the address.
	Refused Class = "refused"
	// Timeout: the address accepted or swallowed the attempt and did not
	// answer in time.
	Timeout Class = "timeout"
	// Discovery: there was no usable address to ask — missing, malformed or
	// unresolvable.
	Discovery Class = "discovery"
	// Unknown: the probe failed for a reason not classified above.
	Unknown Class = "unknown"
)

// Classify says why err kept a probe from getting an answer. A nil error is
// Reachable; an HTTP status is the caller's to report as HTTPStatus, since
// it arrives as a response rather than an error.
func Classify(err error) Class {
	if err == nil {
		return Reachable
	}
	switch {
	case errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EACCES):
		return PermissionDenied
	case errors.Is(err, syscall.ECONNREFUSED):
		return Refused
	case isTimeout(err):
		return Timeout
	case isDiscovery(err):
		return Discovery
	}
	// Some wrappers flatten the errno into text; fall back to its words.
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "operation not permitted"), strings.Contains(msg, "permission denied"):
		return PermissionDenied
	case strings.Contains(msg, "connection refused"):
		return Refused
	}
	return Unknown
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func isDiscovery(err error) bool {
	var dnsErr *net.DNSError
	var addrErr *net.AddrError
	var parseErr *net.ParseError
	if errors.As(err, &dnsErr) || errors.As(err, &addrErr) || errors.As(err, &parseErr) {
		return true
	}
	msg := err.Error()
	for _, marker := range []string{"no Host in request URL", "unsupported protocol scheme", "invalid port", "missing port", "invalid URL"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// Detail is the cause in a form safe to print: the failing call and errno
// ("connect: operation not permitted") without the request URL, which
// carries whatever an operator passed as the address.
func Detail(err error) string {
	if err == nil {
		return ""
	}
	if isTimeout(err) {
		return "no answer within the probe timeout"
	}
	var se *os.SyscallError
	if errors.As(err, &se) {
		return se.Error()
	}
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		err = ue.Err
	}
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Err != nil {
		return oe.Err.Error()
	}
	return err.Error()
}

// ImpliesDown reports whether the class is evidence the daemon is not
// running. Only a refused connection is: the others describe this client's
// view and leave the daemon's state unknown.
func (c Class) ImpliesDown() bool { return c == Refused }

// Summary words what the class establishes, and what it does not.
func (c Class) Summary() string {
	switch c {
	case Reachable:
		return "the daemon answered"
	case HTTPStatus:
		return "the daemon answered with an error status: it is running but not healthy"
	case PermissionDenied:
		return "this client is not permitted to connect to the daemon address (a sandbox or network policy); " +
			"the daemon may be running normally"
	case Refused:
		return "the connection was refused: nothing is listening at the daemon address, so the daemon is most likely not running"
	case Timeout:
		return "the daemon address did not answer in time: the daemon may be busy or hung"
	case Discovery:
		return "there is no usable daemon address to ask: it is missing, malformed or does not resolve"
	default:
		return "this client could not reach the daemon for an unclassified reason; its state is unknown from here"
	}
}

// Remedy says what to do first. restart is the caller's own restart advice
// for this machine — it knows whether a supervisor unit is installed — and
// leads only when the daemon is likely down.
func (c Class) Remedy(restart string) string {
	switch c {
	case Reachable:
		return ""
	case HTTPStatus:
		return "read the daemon log and `tokenops status` from a shell for the failing check; " +
			"restart only if it does not recover: " + restart
	case PermissionDenied:
		return "this client's network access is restricted: check from an unrestricted shell with `tokenops status`, " +
			"or allow this client loopback connections to the daemon address; " +
			"restart only if the daemon is unreachable from there too"
	case Refused:
		return restart
	case Timeout:
		return "check `tokenops daemon status` and the daemon log; if it stays unresponsive, " + restart
	case Discovery:
		return "check where the address comes from: `--addr`, `listen` in the config (`tokenops config show`), " +
			"or the URL hint the daemon writes at startup"
	default:
		return "check from an unrestricted shell with `tokenops status`; if the daemon is unreachable there too, " + restart
	}
}
