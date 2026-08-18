package mcp

// DaemonPresenceNextAction is the remediation appended to next_actions when
// no ingestion daemon is reachable.
const DaemonPresenceNextAction = "start the ingestion daemon with 'tokenops start' (or 'tokenops daemon install' to supervise it)"

// DaemonAlive probes whether an ingestion daemon is reachable, for callers
// outside this package wiring ControlDeps.DaemonAlive. It is the same check
// `mode: active` uses to decide whether activating the mode would be a no-op.
func DaemonAlive() (string, bool) { return daemonAlive() }

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
// A nil probe means the caller could not check. That is not evidence of
// absence, so it stays silent rather than inventing an alarm.
func daemonPresenceWarning(alive func() (string, bool)) string {
	if alive == nil {
		return ""
	}
	if _, ok := alive(); ok {
		return ""
	}
	return "no ingestion daemon is reachable: nothing is writing to the event store, " +
		"so spend and usage answers will go stale without further warning — " +
		"'tokenops serve' is the MCP server and does not ingest; run 'tokenops start'"
}
