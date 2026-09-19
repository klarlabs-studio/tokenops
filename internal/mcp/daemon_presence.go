package mcp

// daemonRemedyEitherWay names the command that brings the daemon back when
// the caller cannot tell whether a supervisor unit is installed. It gives
// both, with the condition that picks between them, and never a foreground
// `tokenops start`: on a supervised machine that starts a second daemon
// beside the one launchd/systemd keeps alive.
const daemonRemedyEitherWay = "run `tokenops daemon restart` if a launchd/systemd unit is installed " +
	"(`tokenops daemon status` says), otherwise `tokenops daemon install` to run it supervised"

// DaemonPresenceNextAction is the remediation appended to next_actions when
// no ingestion daemon is reachable.
const DaemonPresenceNextAction = "bring the ingestion daemon back: " + daemonRemedyEitherWay

// daemonRemedy names the command that brings the daemon back on this
// machine. unitInstalled nil means the caller cannot tell; that gets both
// options rather than a guess.
func daemonRemedy(unitInstalled func() bool) string {
	switch {
	case unitInstalled == nil:
		return daemonRemedyEitherWay
	case unitInstalled():
		return "run `tokenops daemon restart` — a launchd/systemd unit runs the daemon on this machine"
	default:
		return "run `tokenops daemon install` to start it supervised " +
			"(a foreground `tokenops start` also works, until the terminal closes)"
	}
}

// ProbeDaemonAt asks the ingestion daemon how it is doing, for callers
// outside this package wiring ControlDeps.DaemonProbe: first where its URL
// hint says it is, then at fallbackURL, the configured listen address (see
// ConfiguredDaemonURL). It is the same check `mode: active` uses to decide
// whether activating the mode would be a no-op, and it carries the daemon's
// telemetry-loss count back with it.
func ProbeDaemonAt(fallbackURL string) DaemonReport { return probeDaemonAt(fallbackURL) }

// daemonPresenceWarning reports that nothing is ingesting, or "" when a
// daemon answers.
//
// `tokenops serve` and `tokenops start` share nothing but events.db. With the
// daemon absent, serve answers every query successfully against a store that
// has stopped being written — which is how a 27-day outage stayed invisible
// while eleven serve processes ran.
//
// The staleness check catches the consequence, but only after its window has
// elapsed, and a quiet source is genuinely ambiguous: an operator who has not
// used a vendor recently looks identical to one whose poller died. A missing
// daemon is not ambiguous, so this fires immediately and says which of the two
// programs is missing — the pair are one word apart and only one of them
// ingests.
//
// It names `tokenops start` as the program that ingests, not as the fix: the
// warning cannot tell whether a supervisor unit is installed, and where one
// is, a foreground start runs a second daemon beside it.
//
// A nil probe means the caller could not check. That is not evidence of
// absence, so it stays silent rather than inventing an alarm.
// It takes an already-fetched report rather than the probe itself: the drop
// warning needs the same response, and calling the hook here too would mean
// two /healthz round trips to read one payload twice. probed=false means the
// caller had no probe wired.
func daemonPresenceWarning(r DaemonReport, probed bool) string {
	if !probed || r.Alive {
		return ""
	}
	return "no ingestion daemon is reachable: nothing is writing to the event store, " +
		"so spend and usage answers will go stale without further warning — " +
		"`tokenops serve` is the MCP server and does not ingest; the daemon (`tokenops start`) does. " +
		"To bring it back, " + daemonRemedyEitherWay
}
