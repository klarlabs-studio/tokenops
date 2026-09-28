package routeguard

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
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
	kept := st.Proposals[:0]
	for _, p := range st.Proposals {
		rejected, seen := results[p.ToolUseID]
		switch {
		case p.Declined || !seen:
			kept = append(kept, p)
		case rejected:
			p.Declined = true
			kept = append(kept, p)
			out = append(out, ProposalOutcome{Proposal: p})
		default:
			out = append(out, ProposalOutcome{Proposal: p, Approved: true})
		}
	}
	if len(out) > 0 {
		st.Proposals = kept
		saveState(dir, session, st)
	}
	return out
}

// rejectedPrefix is how Claude Code words a tool call the operator
// declined at the permission prompt (verified in 2.1.284).
const rejectedPrefix = "The user doesn't want to proceed with this tool use"

// toolResults maps each tool_use_id in the transcript to whether the
// operator rejected it.
func toolResults(path string) map[string]bool {
	out := map[string]bool{}
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
			Message struct {
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
			out[c.ToolUseID] = c.IsError && strings.Contains(string(c.Content), rejectedPrefix)
		}
	}
	return out
}
