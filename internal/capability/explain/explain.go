// Package explain reconstructs a decision explanation from its append-only
// event history instead of inventing rationale after the fact.
package explain

import (
	"sort"

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
	Outcome      eventschema.OutcomeEvent     `json:"outcome"`
	History      []History                    `json:"history"`
}

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
			r.History = append(r.History, History{EventID: env.ID, At: env.Timestamp.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), Stage: p.Stage})
		case *eventschema.OutcomeEvent:
			outcomeEvents = append(outcomeEvents, env)
		}
	}
	r.Outcome = outcomes.Resolve(outcomeEvents)
	return r, found
}
