package routeguard

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// Proposal is a subagent move the coach asked the operator to approve
// (models: ask). Claude Code shows the rewritten Agent call in its
// permission prompt; Yes runs it, No interrupts the turn and leaves the
// agent waiting for the operator.
type Proposal struct {
	// ID names the proposal in the follow-through ledger.
	ID string `json:"id"`
	// ToolUseID is the Agent call the proposal was attached to. Its
	// result in the transcript says whether it was approved.
	ToolUseID string `json:"tool_use_id"`
	// Call identifies the Agent call by its description and prompt, so a
	// repeat after a decline can be recognised.
	Call string `json:"call"`
	Kind string `json:"kind"`
	From string `json:"from"`
	To   string `json:"to"`
	// Declined marks a proposal the operator said no to. Kept so the
	// agent's retry of the same call runs as planned instead of being
	// asked about again.
	Declined bool `json:"declined,omitempty"`
}

// ProposalOutcome is what became of one proposal.
type ProposalOutcome struct {
	Proposal
	Approved bool
}

// CallKey identifies an Agent call by what it would do.
func CallKey(description, prompt string) string {
	h := sha256.Sum256([]byte(description + "\x00" + prompt))
	return hex.EncodeToString(h[:8])
}

// Propose records a pending proposal for session.
func Propose(dir, session string, p Proposal) {
	if dir == "" || p.ID == "" {
		return
	}
	st := loadState(dir, session)
	st.Proposals = append(st.Proposals, p)
	saveState(dir, session, st)
}

// WasDeclined reports whether the operator already declined moving this
// call in this session, so it should run unchanged.
func WasDeclined(dir, session, call string) bool {
	for _, p := range loadState(dir, session).Proposals {
		if p.Call == call && p.Declined {
			return true
		}
	}
	return false
}

// SettleProposals reads the transcript for the result of each pending
// proposal's Agent call and returns the ones it settles. A declined
// proposal stays on record (marked) so a retry of its call is let
// through; an approved one is dropped.
func SettleProposals(dir, session, transcript string) []ProposalOutcome {
	if dir == "" {
		return nil
	}
	st := loadState(dir, session)
	pending := 0
	for _, p := range st.Proposals {
		if !p.Declined {
			pending++
		}
	}
	if pending == 0 {
		return nil
	}
	results := toolResults(transcript)
	var out []ProposalOutcome
	changed := false
	kept := st.Proposals[:0]
	for _, p := range st.Proposals {
		v, seen := results[p.ToolUseID]
		switch {
		case p.Declined || !seen:
			kept = append(kept, p)
		case v.Outcome == OutcomeVendorStopped:
			// Claude Code refused the call before any prompt: the move was
			// neither approved nor declined, so it is dropped unscored.
			changed = true
		case v.Outcome == OutcomeDeclined:
			p.Declined = true
			kept = append(kept, p)
			out = append(out, ProposalOutcome{Proposal: p})
		default:
			out = append(out, ProposalOutcome{Proposal: p, Approved: true})
		}
	}
	if len(out) > 0 || changed {
		st.Proposals = kept
		saveState(dir, session, st)
	}
	return out
}

// rejectedPrefix is how Claude Code words a tool call the operator
// declined at the permission prompt (verified in 2.1.284 and 2.1.307).
const rejectedPrefix = "The user doesn't want to proceed with this tool use"

// subagentDeclined is how Claude Code words a call a person declined at
// a subagent's permission prompt (verified in 2.1.307).
const subagentDeclined = "Permission for this tool use was denied."

// Outcome is what became of a tool call, as its result in the
// transcript says.
type Outcome int

const (
	// OutcomeRan is a call that ran, whether it then succeeded or failed.
	OutcomeRan Outcome = iota
	// OutcomeDeclined is a call the person declined at the prompt.
	OutcomeDeclined
	// OutcomeVendorStopped is a call Claude Code stopped itself before
	// any prompt: it settles the strictest answer of its rules, auto mode
	// classifier and hooks first, so the person was not asked.
	OutcomeVendorStopped
)

// The guardrails inside Claude Code that stop a call on their own, as
// Verdict.Stopper names them.
const (
	StopperAutoMode       = "auto_mode_classifier"
	StopperPermissionRule = "permission_rule"
	StopperPermission     = "permission_check"
	StopperPlugin         = "plugin"
	StopperHook           = "pre_tool_use_hook"
)

// vendorStops are the result wordings Claude Code gives a call it
// stopped itself, read from 2.1.307 (the auto mode wording also seen in
// a live session). A hook's own permissionDecisionReason is free text and
// cannot be told from a call that ran and failed, so it is not here: it
// still reads as ran.
var vendorStops = []struct {
	prefix, contains, stopper string
}{
	{"Permission for this action was denied by the Claude Code auto mode classifier.", "", StopperAutoMode},
	{"Permission for this action has been denied.", "", StopperPermission},
	{"Permission to use ", " has been denied", StopperPermissionRule},
	{"Permission to use ", " denied by plugin", StopperPlugin},
	{"PreToolUse:", " hook error: ", StopperHook},
}

// Verdict is the outcome of one call, what stopped it when Claude Code
// did, and when its result was written.
type Verdict struct {
	Outcome Outcome
	Stopper string
	At      time.Time
}

// classify reads one tool result.
func classify(isError bool, text string) (Outcome, string) {
	if !isError {
		return OutcomeRan, ""
	}
	t := strings.TrimSpace(text)
	if strings.Contains(t, rejectedPrefix) || strings.HasPrefix(t, subagentDeclined) {
		return OutcomeDeclined, ""
	}
	for _, s := range vendorStops {
		if strings.HasPrefix(t, s.prefix) && strings.Contains(t, s.contains) {
			return OutcomeVendorStopped, s.stopper
		}
	}
	return OutcomeRan, ""
}

// resultText is a tool result's content as text: a string, or the text
// blocks of a list.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	for _, x := range blocks {
		if x.Type == "text" {
			b.WriteString(x.Text)
		}
	}
	return b.String()
}

// toolResults maps each tool_use_id in the transcript to its verdict.
func toolResults(path string) map[string]Verdict {
	out := map[string]Verdict{}
	if path == "" {
		return out
	}
	f, err := os.Open(path) //nolint:gosec // the operator's own transcript
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), `"tool_result"`) {
			continue
		}
		var tl struct {
			Timestamp time.Time `json:"timestamp"`
			Message   struct {
				Content []struct {
					Type      string          `json:"type"`
					ToolUseID string          `json:"tool_use_id"`
					IsError   bool            `json:"is_error"`
					Content   json.RawMessage `json:"content"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &tl) != nil {
			continue
		}
		for _, c := range tl.Message.Content {
			if c.Type != "tool_result" || c.ToolUseID == "" {
				continue
			}
			o, stopper := classify(c.IsError, resultText(c.Content))
			out[c.ToolUseID] = Verdict{Outcome: o, Stopper: stopper, At: tl.Timestamp}
		}
	}
	return out
}

// ToolVerdict reports what became of the call toolUseID; seen is false
// while the transcript holds no result for it. The boundary pause point
// reads its own asks back with it: a call that ran was approved at the
// prompt, a declined one was refused there, and one Claude Code stopped
// itself was never put to the person.
func ToolVerdict(transcript, toolUseID string) (Verdict, bool) {
	if toolUseID == "" {
		return Verdict{}, false
	}
	v, ok := toolResults(transcript)[toolUseID]
	return v, ok
}
