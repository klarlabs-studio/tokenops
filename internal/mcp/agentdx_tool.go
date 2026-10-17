package mcp

import (
	"context"
	"errors"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
)

// AgentDXDeps wires the agent-experience tool. Root empty resolves the
// conventional transcript directory.
type AgentDXDeps struct {
	Root string
}

type agentDXInput struct {
	Days   int    `json:"days,omitempty" jsonschema:"description=Window in days (default 7 when omitted or 0). Use all for every transcript on disk."`
	All    bool   `json:"all,omitempty" jsonschema:"description=Read all history instead of a days window. Overrides days."`
	Source string `json:"source,omitempty" jsonschema:"description=One client: claude-code, codex, cursor or opencode (default: every client present)"`
	Model  string `json:"model,omitempty" jsonschema:"description=Only instructions mostly served by this model id (case-insensitive exact match)"`
	Role   string `json:"role,omitempty" jsonschema:"enum=all,enum=primary,enum=auto_review,enum=subagent,description=all (default), primary (the operator's own instructions), auto_review (reviews the harness ran by itself, such as Codex's), or subagent"`
}

// window is the transcripts and cohort one call selects; an unknown role
// is the caller's error, reported before anything is read.
func cohortWindow(root string, days int, all bool, source, model, role string) (sessions.Window, error) {
	w := sessions.Window{Root: root, Days: days, All: all, Source: source, Model: model, Role: role}
	if _, err := w.Cohort(); err != nil {
		return sessions.Window{}, err
	}
	return w, nil
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
		Description("Measure what the operator's agent sessions are like to work with: turns and wall-clock per instruction, rework rate, interrupt rate, escalation rate, first-try rate, context growth, compactions — each graded, with the single highest-leverage change named. Derived from local transcripts; needs no proxy. Filter by source, model and role to compare the operator's own work with automatic reviews; the cohort block says what the window pooled. Call this when asked how sessions are going, why work feels slow, or before proposing a change to how you and the operator work together.").
		OutputSchema(agentDXResult{}).
		Handler(func(_ context.Context, in agentDXInput) (*agentDXResult, error) {
			w, err := cohortWindow(d.Root, in.Days, in.All, in.Source, in.Model, in.Role)
			if err != nil {
				return nil, err
			}
			res := findings.DX(w, time.Now())
			return &res, nil
		})
	return nil
}
