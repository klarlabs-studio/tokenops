package boundaries

import (
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	infrapresence "go.klarlabs.de/tokenops/internal/infra/presence"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// What the presence check (ADR 0015) found for an approval, recorded on the
// decision as DecisionApproval.Presence. With boundary_approval: channel the
// check is in shadow: the pause point honours approvals as before and
// records what the verifier would have said. With presence enforced an
// approval the check does not honour is recorded as refused, with the
// reason in WouldRefuse, and the decision goes on waiting.
const (
	PresenceSigned      = "signed"
	PresenceHarness     = "harness"
	PresenceTeam        = "team"
	PresenceUnverified  = "unverified"
	PresenceNotEnrolled = "not_enrolled"
	PresenceRefused     = "refused"
)

// spentKept bounds how many used one-step proofs a pending decision
// remembers. A one-step proof is honoured for ten minutes, so a handful
// covers any replay inside its window.
const spentKept = 16

// presenceDir is the presence key store: Env.PresenceDir, or presence/
// beside the boundary ledger (~/.tokenops/presence).
func (e Env) presenceDir() infrapresence.Dir {
	if e.PresenceDir != "" {
		return infrapresence.Dir(e.PresenceDir)
	}
	return infrapresence.Dir(filepath.Join(filepath.Dir(e.Dir.Root()), "presence"))
}

// newChallenge is a fresh nonce for one raising of a decision.
func newChallenge() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return hex.EncodeToString(b)
}

// presenceCheck is the verifier's view of one approval.
type presenceCheck struct {
	presence, key, wouldRefuse string
	// spent is a valid one-step proof's fingerprint, remembered once the
	// approval is used.
	spent string
}

// honoured reports whether an approval with this check counts under mode:
// always under channel; under presence when it is signed, attested by the
// harness's prompt or the team plane's; under presence-strict, not the
// prompt.
func (c presenceCheck) honoured(mode presence.Mode) bool {
	if !mode.Enforced() {
		return true
	}
	switch c.presence {
	case PresenceSigned, PresenceTeam:
		return true
	case PresenceHarness:
		return !mode.Strict()
	}
	return false
}

// refusal is the reason an approval was not honoured under mode, for the
// record and for the refusal the agent reads.
func (c presenceCheck) refusal(mode presence.Mode) presence.Reason {
	switch {
	case c.presence == PresenceHarness && mode.Strict():
		return reasonHarnessStrict
	case c.presence == PresenceNotEnrolled:
		return presence.ReasonNotEnrolled
	case c.wouldRefuse != "":
		return presence.Reason(c.wouldRefuse)
	}
	return presence.ReasonUnsigned
}

// reasonHarnessStrict: a prompt approval under presence-strict.
const reasonHarnessStrict presence.Reason = "harness_not_accepted"

// reasonPhrase is a reason in words, including the ones only enforcement
// gives.
func reasonPhrase(r presence.Reason) string {
	if r == reasonHarnessStrict {
		return "boundary_approval is presence-strict, so Claude Code's prompt does not approve; only a signed approval does"
	}
	return r.Phrase()
}

func (c presenceCheck) stamp(a eventschema.DecisionApproval) eventschema.DecisionApproval {
	a.Presence, a.PresenceKey, a.WouldRefuse = c.presence, c.key, c.wouldRefuse
	return a
}

// checkPresence evaluates an approval the pause point is about to honour
// against the enrolled presence key, for the decision as the pause point
// computed it (id, rule and session come from the call, not the answer
// file). It changes nothing about the answer.
//
// A prompt approval is harness-attested and a team approval the team
// plane's; neither carries a proof. With no key enrolled the check reads
// one missing file and reports not_enrolled, which status says once
// rather than on every approval.
func checkPresence(env Env, a boundaryledger.Answer, id, rule, session string, now time.Time) presenceCheck {
	switch a.Method {
	case MethodPrompt:
		return presenceCheck{presence: PresenceHarness}
	case MethodTeam:
		return presenceCheck{presence: PresenceTeam}
	}
	trust, err := env.presenceDir().Trust(now)
	if err != nil {
		return presenceCheck{presence: PresenceUnverified, wouldRefuse: string(presence.ReasonKeyUnreadable)}
	}
	if trust.State == presence.TrustNotEnrolled {
		return presenceCheck{presence: PresenceNotEnrolled}
	}
	p, _, _ := env.Dir.GetPending(id)
	res := presence.Verify(a.Proof, presence.Expect{
		Decision: id, Rule: rule, Session: session, Scope: a.Scope, Challenge: p.Challenge,
		Crossings: a.Crossings, Now: now,
		Spent: func(fp string) bool { return slices.Contains(p.Spent, fp) },
	}, trust)
	if !res.OK() {
		return presenceCheck{presence: PresenceUnverified, key: res.KeyID, wouldRefuse: string(res.Reason)}
	}
	out := presenceCheck{presence: PresenceSigned, key: res.KeyID}
	if a.Scope == boundaryledger.ScopeOnce {
		out.spent = a.Proof.Fingerprint()
	}
	return out
}

// retireChallenge closes the raising a one-step approval answered: the
// pending decision gets a fresh challenge, so a proof for this raising
// answers no later one, and a signed proof's fingerprint is remembered.
// A decision with no pending record (answered before it was written, or
// by a team) has nothing to retire.
func retireChallenge(env Env, id, spent string) error {
	p, ok, err := env.Dir.GetPending(id)
	if err != nil || !ok {
		return err
	}
	p.Challenge = newChallenge()
	if spent != "" {
		p.Spent = append(p.Spent, spent)
		if len(p.Spent) > spentKept {
			p.Spent = p.Spent[len(p.Spent)-spentKept:]
		}
	}
	return env.Dir.PutPending(p)
}

// presenceRationale is what a decision's rationale says about an approval
// the verifier would not have honoured, in the words other shadow
// outcomes use.
func presenceRationale(a eventschema.DecisionApproval) string {
	switch {
	case a.Presence == PresenceRefused:
		return "; presence enforced: this " + a.Method + " approval was not honoured (" +
			reasonPhrase(presence.Reason(a.WouldRefuse)) + "), so the decision still waits (unsigned_answer_ignored)"
	case a.Presence == PresenceSigned:
		return "; approved in person: signed with presence key " + a.PresenceKey +
			" (Touch ID, a paired Apple Watch or the login password); if you did not approve that dialog, deny this decision"
	case a.WouldRefuse == "":
		return ""
	}
	return "; presence check in shadow: with presence enforced this " + a.Method + " approval would not be honoured (" +
		presence.Reason(a.WouldRefuse).Phrase() + "), and it changed nothing"
}

// Presence states status reports.
const (
	PresenceStateNotEnrolled = "not_enrolled"
	PresenceStateShadow      = "shadow"
	PresenceStateHeld        = "held"
	// PresenceStateEnforced: boundary_approval is presence or
	// presence-strict and a key is enrolled.
	PresenceStateEnforced = "enforced"
	// PresenceStateUnavailable: presence is enforced and no key is
	// enrolled, so nothing can approve but the harness's prompt (and, with
	// presence-strict, nothing at all).
	PresenceStateUnavailable = "unavailable"
)

// PresenceStatus is whether this machine can prove an approver present,
// and what the check found.
type PresenceStatus struct {
	State string `json:"state"`
	// Approval is the boundary_approval setting: channel, presence or
	// presence-strict.
	Approval string `json:"approval"`
	KeyID    string `json:"key_id,omitempty"`
	// Since is when the key was pinned; HoldUntil, when an unsigned
	// replacement's hold ends.
	Since     *time.Time `json:"since,omitempty"`
	HoldUntil *time.Time `json:"hold_until,omitempty"`
	// WouldBeIgnored counts approvals honoured in shadow that enforcement
	// would not have honoured; Refused, approvals enforcement did not
	// honour; HarnessAttested, approvals given at the harness's prompt.
	WouldBeIgnored  int    `json:"would_be_ignored"`
	Refused         int    `json:"refused"`
	HarnessAttested int    `json:"harness_attested"`
	Note            string `json:"note"`
	// Enrol is the command that enrols a key, when none is.
	Enrol string `json:"enrol,omitempty"`
}

// EnrolCommand enrols this Mac's presence key.
const EnrolCommand = "tokenops boundaries presence enroll"

// Presence reads the key store and, when a key is enrolled, counts what
// the check recorded in the ledger. With no key it reads one missing file
// and says so once. A replaced key's hold starts when it is first read,
// here or at the pause point.
func Presence(env Env) PresenceStatus {
	now := env.now()
	mode := env.approval()
	trust, err := env.presenceDir().Trust(now)
	if err != nil {
		state := PresenceStateShadow
		if mode.Enforced() {
			state = PresenceStateUnavailable
		}
		return PresenceStatus{State: state, Approval: string(mode), Note: "the presence key store could not be read: " + failureClass(err)}
	}
	if trust.State == presence.TrustNotEnrolled {
		return notEnrolledStatus(mode)
	}
	since := trust.Pin.Since
	out := PresenceStatus{State: PresenceStateShadow, Approval: string(mode), KeyID: trust.Key.ID, Since: &since}
	if mode.Enforced() {
		out.State = PresenceStateEnforced
	}
	if trust.State == presence.TrustHeld {
		hold := trust.Pin.HoldUntil
		out.State, out.HoldUntil = PresenceStateHeld, &hold
	}
	out.WouldBeIgnored, out.Refused, out.HarnessAttested = countPresence(env.Dir)
	out.Note = presenceNote(out, trust, mode)
	return out
}

func notEnrolledStatus(mode presence.Mode) PresenceStatus {
	if mode.Enforced() {
		note := "boundary_approval is " + string(mode) + " but no presence key is enrolled on this machine: " +
			"`tokenops approve` and the API cannot approve anything (deny still works"
		if !mode.Strict() {
			note += ", and so does Claude Code's prompt"
		}
		return PresenceStatus{
			State: PresenceStateUnavailable, Approval: string(mode), Enrol: EnrolCommand,
			Note: note + "); enrol a key with `" + EnrolCommand + "`, or set boundary_approval: channel",
		}
	}
	return PresenceStatus{
		State: PresenceStateNotEnrolled, Approval: string(mode), Enrol: EnrolCommand,
		Note: "presence approval not enrolled: approvals are trusted by the channel they arrive on " +
			"(your terminal, Claude Code's prompt), not proven to come from a person at this machine",
	}
}

func presenceNote(s PresenceStatus, t presence.Trust, mode presence.Mode) string {
	if s.State == PresenceStateHeld {
		effect := "(in shadow, nothing is refused)"
		if mode.Enforced() {
			effect = "(approvals are refused until then)"
		}
		return "approval key replaced at " + t.Pin.Since.Format(time.RFC3339) +
			" without the old key's signature; signed approvals are held until " + s.HoldUntil.Format(time.RFC3339) + " " + effect
	}
	var note string
	switch mode {
	case presence.ModePresence:
		note = "presence key " + s.KeyID + " enrolled and enforced: an approval counts only when signed with it " +
			"(Touch ID, a paired Apple Watch or your password) or given at Claude Code's prompt"
	case presence.ModeStrict:
		note = "presence key " + s.KeyID + " enrolled and enforced (strict): an approval counts only when signed with it " +
			"(Touch ID, a paired Apple Watch or your password); Claude Code's prompt does not approve"
	default:
		note = "presence key " + s.KeyID + " enrolled; in shadow, approvals are checked and nothing is refused " +
			"(boundary_approval: presence enforces it)"
	}
	if s.WouldBeIgnored > 0 {
		note += "; " + strconv.Itoa(s.WouldBeIgnored) + " approval(s) would not have been honoured with presence enforced (`tokenops explain <id>` says why)"
	}
	if s.Refused > 0 {
		note += "; " + strconv.Itoa(s.Refused) + " approval(s) were not honoured (`tokenops explain <id>` says why)"
	}
	return note
}

// countPresence counts distinct approvals the check recorded as
// unverified in shadow, refused under enforcement, and attested by the
// harness.
func countPresence(dir boundaryledger.Dir) (wouldBeIgnored, refused, harness int) {
	envs, err := dir.Read()
	if err != nil {
		return 0, 0, 0
	}
	seen := map[string]bool{}
	for _, env := range envs {
		d, ok := env.Payload.(*eventschema.DecisionEvent)
		if !ok || d.Approval.Presence == "" {
			continue
		}
		key := env.Correlation.Decision + "|" + d.Approval.At.UTC().Format(time.RFC3339Nano)
		if seen[key] {
			continue
		}
		seen[key] = true
		switch d.Approval.Presence {
		case PresenceUnverified:
			wouldBeIgnored++
		case PresenceRefused:
			refused++
		case PresenceHarness:
			harness++
		}
	}
	return wouldBeIgnored, refused, harness
}
