package findings

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	"go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/capability/coach"
)

// The actions a surface can run on a finding: a closed allowlist, each one
// an operation the CLI already has. A finding offers only those that apply
// to it now.
const (
	// ActBoundaryPromote makes the finding's boundary bind
	// (`tokenops boundaries promote <rule>`).
	ActBoundaryPromote = "boundary.promote"
	// ActTipAck, ActTipExpected and ActTipSnooze answer the finding's tip
	// (`tokenops coach tip ack|expected|snooze`).
	ActTipAck      = "tip.ack"
	ActTipExpected = "tip.expected"
	ActTipSnooze   = "tip.snooze"
)

// tipAnswers maps each tip action to the answer it records.
var tipAnswers = map[string]string{
	ActTipAck:      coach.AnswerAcknowledged,
	ActTipExpected: coach.AnswerExpected,
	ActTipSnooze:   coach.AnswerSnoozed,
}

// Allowed reports whether id is an action on the allowlist.
func Allowed(id string) bool {
	_, tip := tipAnswers[id]
	return tip || id == ActBoundaryPromote
}

// FindingAction is one operation a surface can offer on a finding.
type FindingAction struct {
	// ID is from the allowlist: boundary.promote, tip.ack, tip.expected
	// or tip.snooze.
	ID string `json:"id"`
	// Label is the button's text.
	Label string `json:"label"`
	// Confirm says what will change, to be shown and agreed to before the
	// action runs; empty for an answer that binds nothing.
	Confirm string `json:"confirm,omitempty"`
}

// ErrStale is an action on a finding that is no longer shown, or that no
// longer offers it: the surface is showing an older reading.
var ErrStale = errors.New("the finding has changed")

// What answering "expected" changes, said before it is recorded.
const (
	expectedSpend = "Spend like this stops being flagged: the same pattern (the same pricier model, " +
		"the same plan billed per token, a loop in the same repository) is taken as normal from now on."
	expectedNewestTip = "The newest tip is recorded as expected: enough expected answers move the session " +
		"budget up, read off your p95 session instead of your p90."
)

// tipActions are the three answers to a tip; expected names what it changes.
func tipActions(expected string) []FindingAction {
	return []FindingAction{
		{ID: ActTipAck, Label: "Got it"},
		{ID: ActTipExpected, Label: "Expected", Confirm: expected},
		{ID: ActTipSnooze, Label: fmt.Sprintf("Snooze %s", snoozeWords(coach.DefaultSnooze))},
	}
}

func snoozeWords(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		if d == 24*time.Hour {
			return "a day"
		}
		return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
	}
	return fmt.Sprintf("%dh", int(d/time.Hour))
}

// promoteAction offers to make the headroom pause bind, in the rule's own
// condition and with the way back.
func promoteAction(p boundaries.PauseSummary) FindingAction {
	when := p.When
	if when == "" {
		when = "its condition holds"
	}
	what := "pause sessions"
	switch p.Require {
	case "deny":
		what = "refuse the next tool call"
	case "approver":
		what = "pause sessions for an approver"
	}
	return FindingAction{ID: ActBoundaryPromote, Label: "Promote pause",
		Confirm: fmt.Sprintf("%s will %s when %s. Undo with `tokenops boundaries demote %s`.", p.Rule, what, when, p.Rule)}
}

// key names a finding by its kind and subject: lower case, words joined
// by "-", parts by ":".
func key(kind string, subject ...string) string {
	parts := []string{kind}
	for _, s := range subject {
		if s = slug(s); s != "" {
			parts = append(parts, s)
		}
	}
	k := strings.Join(parts, ":")
	if len(k) > maxKey {
		k = k[:maxKey]
	}
	return k
}

// maxKey bounds a key's length.
const maxKey = 128

func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// shortHash names a subject that has no shorter stable name.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

// uniqueKeys suffixes a key seen earlier in out, so each names one finding.
func uniqueKeys(out []Finding) {
	seen := map[string]int{}
	for i := range out {
		k := out[i].Key
		seen[k]++
		if n := seen[k]; n > 1 {
			out[i].Key = fmt.Sprintf("%s-%d", k, n)
		}
	}
}

// Find is the finding keyed key.
func (r Report) Find(key string) (Finding, bool) {
	for _, f := range r.Findings {
		if f.Key == key {
			return f, true
		}
	}
	return Finding{}, false
}

// ActEnv is what running an action needs.
type ActEnv struct {
	// ConfigPath is the config a promote writes.
	ConfigPath string
	// Ledger is the coach's follow-through ledger a tip answer is
	// written to.
	Ledger coach.Ledger
	Now    time.Time
}

// Acted is what an action did.
type Acted struct {
	Key    string `json:"key"`
	Action string `json:"action"`
	// Rule, Stage and Config are set by a promote.
	Rule   string `json:"rule,omitempty"`
	Stage  string `json:"stage,omitempty"`
	Config string `json:"config,omitempty"`
	// Tip, Answer and SnoozedUntil are set by a tip answer.
	Tip          string     `json:"tip,omitempty"`
	Answer       string     `json:"answer,omitempty"`
	SnoozedUntil *time.Time `json:"snoozed_until,omitempty"`
	// ConfigChanged is whether config.yaml was written.
	ConfigChanged bool   `json:"config_changed"`
	Note          string `json:"note,omitempty"`
}

// Act runs action on the finding keyed key in r, through the same
// capability the CLI calls. It refuses an action off the allowlist as
// input, and a finding r does not show, or that does not offer action,
// as ErrStale: r should be the findings as they are now.
func Act(r Report, key, action string, env ActEnv) (Acted, error) {
	if !Allowed(action) {
		return Acted{}, actions.InputError{Err: fmt.Errorf("%q is not an action on a finding", action)}
	}
	f, ok := r.Find(key)
	if !ok {
		return Acted{}, fmt.Errorf("%w: no finding %q is shown now", ErrStale, key)
	}
	if !offers(f, action) {
		return Acted{}, fmt.Errorf("%w: %q does not offer %s now", ErrStale, key, action)
	}
	out := Acted{Key: key, Action: action}
	if action == ActBoundaryPromote {
		p, err := boundaries.Promote(env.ConfigPath, f.rule)
		if err != nil {
			return Acted{}, err
		}
		out.Rule, out.Stage, out.Config, out.Note, out.ConfigChanged = p.Rule, p.Stage, p.Config, p.Note, true
		return out, nil
	}
	a, err := coach.AnswerTip(env.Ledger, env.Now, f.ID, tipAnswers[action], 0)
	if errors.Is(err, coach.ErrNoTip) {
		return Acted{}, fmt.Errorf("%w: the coach has no tip you have not answered", ErrStale)
	}
	if err != nil {
		return Acted{}, err
	}
	out.Tip, out.Answer, out.SnoozedUntil = a.ID, a.Answer, a.SnoozedUntil
	return out, nil
}

func offers(f Finding, action string) bool {
	for _, a := range f.Actions {
		if a.ID == action {
			return true
		}
	}
	return false
}
