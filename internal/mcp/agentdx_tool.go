package mcp

import (
	"context"
	"errors"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
)

// AgentDXDeps wires the agent-experience tool. Root empty resolves the
// conventional transcript directory.
type AgentDXDeps struct {
	Root string
}

type agentDXInput struct {
	Days int `json:"days,omitempty" jsonschema:"description=Window in days (default 7). 0 reads all history."`
}

// agentDXResult is the typed payload for tokenops_agent_dx.
type agentDXResult struct {
	Window         string            `json:"window"`
	Metrics        agentdx.Metrics   `json:"metrics"`
	Grades         agentdx.Grades    `json:"grades"`
	Recommendation *dxRecommendation `json:"recommendation,omitempty"`
	Note           string            `json:"note,omitempty"`
}

type dxRecommendation struct {
	Title    string `json:"title"`
	Evidence string `json:"evidence"`
	Action   string `json:"action"`
}

// RegisterAgentDXTools exposes the agent-experience metrics to the agent
// itself.
//
// The metrics existed only as a CLI report, which meant they were seen
// when the operator remembered to look. Handing them to the agent is what
// makes them act on the session they describe: an agent that can see it
// is on its eleventh turn of one instruction, with a quarter of its edits
// revisiting files it already changed, can say so and ask for a sharper
// brief instead of pressing on.
func RegisterAgentDXTools(s *Server, d AgentDXDeps) error {
	if s == nil {
		return errors.New("mcp: nil server")
	}
	s.Tool("tokenops_agent_dx").
		Description("Measure what the operator's agent sessions are like to work with: turns and wall-clock per instruction, rework rate, interrupt rate, escalation rate, first-try rate, context growth, compactions — each graded, with the single highest-leverage change named. Derived from local transcripts; needs no proxy. Call this when asked how sessions are going, why work feels slow, or before proposing a change to how you and the operator work together.").
		OutputSchema(agentDXResult{}).
		Handler(func(_ context.Context, in agentDXInput) (*agentDXResult, error) {
			days := in.Days
			if days == 0 {
				days = 7
			}
			opts := agentdx.ExtractOptions{Root: d.Root}
			window := "all history"
			if days > 0 {
				opts.Since = time.Now().AddDate(0, 0, -days)
				window = formatDays(days)
			}
			records, err := agentdx.ExtractAll(opts)
			if err != nil {
				return nil, err
			}
			m := agentdx.Compute(records)
			out := &agentDXResult{Window: window, Metrics: m, Grades: agentdx.Grade(m)}
			if m.Prompts == 0 {
				out.Note = "no instructions in this window — widen with days, or check the transcript root"
				return out, nil
			}
			if rec, ok := agentdx.Recommend(m); ok {
				out.Recommendation = &dxRecommendation{
					Title: rec.Title, Evidence: rec.Evidence, Action: rec.Action,
				}
			} else {
				out.Note = "nothing stands out — every measured dimension grades well"
			}
			return out, nil
		})
	return nil
}

func formatDays(d int) string {
	if d == 1 {
		return "last 1d"
	}
	return "last " + itoa(d) + "d"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
