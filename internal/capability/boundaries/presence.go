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
// decision as DecisionApproval.Presence. Presence is in shadow: the pause
// point honours approvals as before and records what the verifier would
// have said.
const (
	PresenceSigned      = "signed"
	PresenceHarness     = "harness"
	PresenceTeam        = "team"
	PresenceUnverified  = "unverified"
	PresenceNotEnrolled = "not_enrolled"
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
	if a.WouldRefuse == "" {
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
)

// PresenceStatus is whether this machine can prove an approver present,
// and what the shadow check found.
type PresenceStatus struct {
	State string `json:"state"`
	KeyID string `json:"key_id,omitempty"`
	// Since is when the key was pinned; HoldUntil, when an unsigned
	// replacement's hold ends.
	Since     *time.Time `json:"since,omitempty"`
	HoldUntil *time.Time `json:"hold_until,omitempty"`
	// WouldBeIgnored counts approvals honoured that enforcement would not
	// have honoured; HarnessAttested, approvals given at the harness's
	// prompt, which keep counting under enforcement.
	WouldBeIgnored  int    `json:"would_be_ignored"`
	HarnessAttested int    `json:"harness_attested"`
	Note            string `json:"note"`
}

// Presence reads the key store and, when a key is enrolled, counts what
// the shadow check recorded in the ledger. With no key it reads one
// missing file and says so once. A replaced key's hold starts when it is
// first read, here or at the pause point.
func Presence(env Env) PresenceStatus {
	now := env.now()
	trust, err := env.presenceDir().Trust(now)
	if err != nil {
		return PresenceStatus{State: PresenceStateShadow, Note: "the presence key store could not be read: " + failureClass(err)}
	}
	if trust.State == presence.TrustNotEnrolled {
		return PresenceStatus{
			State: PresenceStateNotEnrolled,
			Note: "presence approval not enrolled: approvals are trusted by the channel they arrive on " +
				"(your terminal, Claude Code's prompt), not proven to come from a person at this machine",
		}
	}
	since := trust.Pin.Since
	out := PresenceStatus{State: PresenceStateShadow, KeyID: trust.Key.ID, Since: &since}
	if trust.State == presence.TrustHeld {
		hold := trust.Pin.HoldUntil
		out.State, out.HoldUntil = PresenceStateHeld, &hold
	}
	out.WouldBeIgnored, out.HarnessAttested = countPresence(env.Dir)
	out.Note = presenceNote(out, trust)
	return out
}

func presenceNote(s PresenceStatus, t presence.Trust) string {
	if s.State == PresenceStateHeld {
		return "approval key replaced at " + t.Pin.Since.Format(time.RFC3339) +
			" without the old key's signature; signed approvals are held until " + s.HoldUntil.Format(time.RFC3339) +
			" (in shadow, nothing is refused)"
	}
	note := "presence key " + s.KeyID + " enrolled; in shadow, approvals are checked and nothing is refused"
	if s.WouldBeIgnored > 0 {
		note += "; " + strconv.Itoa(s.WouldBeIgnored) + " approval(s) would not have been honoured with presence enforced (`tokenops explain <id>` says why)"
	}
	return note
}

// countPresence counts distinct approvals the shadow check recorded as
// unverified, and those the harness attested.
func countPresence(dir boundaryledger.Dir) (wouldBeIgnored, harness int) {
	envs, err := dir.Read()
	if err != nil {
		return 0, 0
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
		case PresenceHarness:
			harness++
		}
	}
	return wouldBeIgnored, harness
}
