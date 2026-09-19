package events

import "fmt"

// DropNextAction is the remediation offered whenever the daemon has failed
// to persist telemetry.
//
// It names the log rather than a fix because the cause is in the error the
// sink returned — a locked database reads nothing like a full disk — and a
// remediation that guesses would send an operator to the wrong one.
const DropNextAction = "check the daemon log for 'append batch gave up, rows lost' to see why the writes failed; " +
	"restarting the daemon ('tokenops daemon restart') clears the counter but not the cause"

// DropWarning renders the operator-facing warning for telemetry rows the
// daemon could not persist, or "" when none were lost.
//
// This exists because the counter had exactly one reader: a log line at
// shutdown. A bus that gave up on a batch incremented a number nothing ever
// showed, so 30,254 rows went missing across five weeks while `tokenops
// status` reported healthy the whole time — the same shape of silence the
// counter had been added to end.
//
// Kept here so the CLI status command and the MCP status tool emit
// byte-identical strings.
func DropWarning(n int64) string {
	if n <= 0 {
		return ""
	}
	noun := "events"
	if n == 1 {
		noun = "event"
	}
	return fmt.Sprintf(
		"telemetry dropped: %d %s failed to persist since the daemon started — "+
			"spend and usage totals under-report by that much, and a restart clears the count but not the cause",
		n, noun)
}
