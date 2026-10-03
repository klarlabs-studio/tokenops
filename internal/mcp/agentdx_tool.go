package mcp

import (
	"context"
	"errors"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/sessions"
)

// AgentDXDeps wires the agent-experience tool. Root empty resolves the
// conventional transcript directory.
type AgentDXDeps struct {
	Root string
}

type agentDXInput struct {
	Days int  `json:"days,omitempty" jsonschema:"description=Window in days (default 7 when omitted or 0). Use all for every transcript on disk."`
	All  bool `json:"all,omitempty" jsonschema:"description=Read all history instead of a days window. Overrides days."`
}

// agentDXResult is the sessions capability's payload, shared with the
// daemon API (ADR 0010 §4).
type agentDXResult = sessions.DX

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
			res := sessions.ComputeDX(sessions.Window{Root: d.Root, Days: in.Days, All: in.All}, time.Now())
			return &res, nil
		})
	return nil
}
