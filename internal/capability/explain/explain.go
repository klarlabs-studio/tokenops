// Package explain reconstructs a decision explanation from its append-only
// event history instead of inventing rationale after the fact.
package explain

import (
	"sort"
	"strings"

	"go.klarlabs.de/tokenops/internal/capability/outcomes"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Report is the complete explain-on-demand view of one decision.
type Report struct {
	DecisionID   string                       `json:"decision_id"`
	Stage        eventschema.DecisionStage    `json:"stage"`
	Kind         string                       `json:"kind"`
	Selected     eventschema.ResourceOption   `json:"selected"`
	Alternatives []eventschema.ResourceOption `json:"alternatives"`
	Evidence     []eventschema.EvidenceRef    `json:"evidence"`
	Policy       string                       `json:"policy,omitempty"`
	Authority    string                       `json:"authority,omitempty"`
	Confidence   float64                      `json:"confidence,omitempty"`
	Rationale    string                       `json:"rationale"`
	Executable   bool                         `json:"executable"`
	Adapter      string                       `json:"adapter,omitempty"`
	Fingerprint  string                       `json:"fingerprint,omitempty"`
	// Rule is the boundary rule that made the decision, for a boundary
	// decision (ADR 0014).
	Rule string `json:"rule,omitempty"`
	// Actor is who asked for the action.
	Actor eventschema.DecisionActor `json:"actor,omitzero"`
	// Approval is who answered, once someone has.
	Approval eventschema.DecisionApproval `json:"approval,omitzero"`
	Outcome  eventschema.OutcomeEvent     `json:"outcome"`
	History  []History                    `json:"history"`
}

// BoundaryPolicyPrefix opens the Policy of a decision a boundary rule
// made; the rest is the rule's name.
const BoundaryPolicyPrefix = "boundary:"

// History is one lifecycle transition.
type History struct {
	EventID string                    `json:"event_id"`
	At      string                    `json:"at"`
	Stage   eventschema.DecisionStage `json:"stage"`
}

// Build folds decision and outcome records. The last decision transition is
// current; outcome strength is resolved independently.
func Build(decisionID string, events []*eventschema.Envelope) (Report, bool) {
	sort.SliceStable(events, func(i, j int) bool { return events[i].Timestamp.Before(events[j].Timestamp) })
	r := Report{DecisionID: decisionID}
	var outcomeEvents []*eventschema.Envelope
	var found bool
	for _, env := range events {
		if env == nil || env.Correlation.Decision != decisionID {
			continue
		}
		switch p := env.Payload.(type) {
		case *eventschema.DecisionEvent:
			found = true
			r.Stage, r.Kind, r.Selected = p.Stage, p.Kind, p.Selected
			r.Alternatives, r.Evidence = p.Alternatives, p.Evidence
			r.Policy, r.Authority, r.Confidence = p.Policy, p.Authority, p.Confidence
			r.Rationale, r.Executable, r.Adapter, r.Fingerprint = p.Rationale, p.Executable, p.Adapter, p.Fingerprint
			if p.Actor != (eventschema.DecisionActor{}) {
				r.Actor = p.Actor
			}
			if !p.Approval.At.IsZero() || p.Approval.Approver != "" {
				r.Approval = p.Approval
			}
			if rule, ok := strings.CutPrefix(p.Policy, BoundaryPolicyPrefix); ok {
				r.Rule = rule
			}
			r.History = append(r.History, History{EventID: env.ID, At: env.Timestamp.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), Stage: p.Stage})
		case *eventschema.OutcomeEvent:
			outcomeEvents = append(outcomeEvents, env)
		}
	}
	r.Outcome = outcomes.Resolve(outcomeEvents)
	return r, found
}
