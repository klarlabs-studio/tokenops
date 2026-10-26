package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"
)

// The panel acts on the coach's findings through the daemon
// (POST /api/findings/act and /api/findings/dismiss), which checks that
// the finding still offers the action, runs it through the same code as
// the CLI and audits it. The panel names a finding by its key and an
// action by its id; both are checked here before they reach the daemon.

var findingKey = regexp.MustCompile(`^[a-z0-9][a-z0-9:._-]{0,127}$`)

// findingActions is the daemon's allowlist of actions on a finding.
var findingActions = map[string]bool{
	"boundary.promote": true, "tip.ack": true, "tip.expected": true, "tip.snooze": true,
}

// dismissFor is how long the panel can hide a finding; none shows it again.
var dismissFor = map[string]bool{"day": true, "week": true, "none": true}

// errBadFinding is a key, action or duration the panel should never send.
var errBadFinding = errors.New("not a finding's key, action or duration")

func validFindingKey(k string) bool { return findingKey.MatchString(k) }

// findingActInput is the finding.act command's argument.
type findingActInput struct {
	Key    string `json:"key"`
	Action string `json:"action"`
}

// findingDismissInput is the finding.dismiss command's argument; an empty
// key with for "none" shows every hidden finding again.
type findingDismissInput struct {
	Key string `json:"key"`
	For string `json:"for"`
}

func (in findingActInput) valid() bool {
	return validFindingKey(in.Key) && findingActions[in.Action]
}

func (in findingDismissInput) valid() bool {
	if !dismissFor[in.For] {
		return false
	}
	if in.Key == "" {
		return in.For == "none"
	}
	return validFindingKey(in.Key)
}

// acted is the part of the daemon's answer the note is made from.
type acted struct {
	Action       string     `json:"action"`
	Rule         string     `json:"rule"`
	Answer       string     `json:"answer"`
	SnoozedUntil *time.Time `json:"snoozed_until"`
}

// actOnFinding runs an action a finding offers.
func (d *daemon) actOnFinding(ctx context.Context, in findingActInput) (acted, error) {
	var out struct {
		Acted acted `json:"acted"`
	}
	err := d.do(ctx, http.MethodPost, "/api/findings/act", in, &out)
	return out.Acted, err
}

// dismissFinding hides a finding for a day or a week, or shows it again.
func (d *daemon) dismissFinding(ctx context.Context, in findingDismissInput) error {
	var out json.RawMessage
	return d.do(ctx, http.MethodPost, "/api/findings/dismiss", in, &out)
}

// errStaleFinding is the daemon saying the finding has changed since the
// panel read it.
func errStaleFinding(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.code == http.StatusConflict
}

// staleNote is said when the panel acted on an older reading.
const staleNote = "That finding changed since the panel read it, so nothing was done. Here it is as it is now."

// actOnFinding runs in through the daemon and leaves what it did in the
// panel's note. A finding that changed meanwhile is a note too, not an
// error: the refresh that follows shows it as it is.
func (m *menubar) actOnFinding(ctx context.Context, in findingActInput) error {
	if !in.valid() {
		return errBadFinding
	}
	a, err := m.daemon.actOnFinding(ctx, in)
	switch {
	case errStaleFinding(err):
		m.setNote(staleNote, true)
		return nil
	case err != nil:
		return err
	}
	m.setNote(actedNote(a), false)
	return nil
}

// dismissFinding hides or shows a finding through the daemon.
func (m *menubar) dismissFinding(ctx context.Context, in findingDismissInput) error {
	if !in.valid() {
		return errBadFinding
	}
	err := m.daemon.dismissFinding(ctx, in)
	switch {
	case errStaleFinding(err):
		m.setNote(staleNote, true)
		return nil
	case err != nil:
		return err
	}
	switch in.For {
	case "day":
		m.setNote("Hidden for a day, or until it becomes more urgent.", false)
	case "week":
		m.setNote("Hidden for a week, or until it becomes more urgent.", false)
	default:
		m.setNote("Showing every finding again.", false)
	}
	return nil
}

// actedNote says what an action did, in the panel's words.
func actedNote(a acted) string {
	switch a.Action {
	case "boundary.promote":
		return fmt.Sprintf("%s now binds from the next tool call; tokenops boundaries demote %s puts it back in shadow.", a.Rule, a.Rule)
	case "tip.snooze":
		if a.SnoozedUntil != nil {
			return "Snoozed: tips like it wait until " + a.SnoozedUntil.Local().Format("15:04") + "."
		}
		return "Snoozed."
	case "tip.expected":
		return "Recorded as expected."
	}
	return "Answered: got it."
}
