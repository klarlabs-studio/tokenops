// Package outcomes turns explicit feedback and local verifier results into
// provenance-carrying OutcomeEvents.
package outcomes

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	toolcoach "go.klarlabs.de/tokenops/internal/contexts/coaching/tools"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Channel is how a reported outcome reached TokenOps. It decides what the
// outcome can vouch for: only an operator's own surface records a human
// judgement. An agent that says how its own work went is a self-report,
// however sure it sounds, and learning does not count it as strong evidence.
type Channel string

const (
	// ViaCLI is `tokenops outcome record`, run at the operator's terminal.
	ViaCLI Channel = "cli"
	// ViaAPI is the daemon's POST /api/outcomes, behind the API token, for
	// the operator's own surfaces (ADR 0010 §6).
	ViaAPI Channel = "api"
	// ViaOperatorConfirmed is an agent's report over MCP that the operator
	// confirmed, or corrected, at the client through elicitation.
	ViaOperatorConfirmed Channel = "mcp_operator_confirmed"
	// ViaAgent is an agent's own report over MCP that no operator confirmed.
	ViaAgent Channel = "mcp_agent"
)

// ReportedOutcomeKind is the evidence kind naming the channel a reported
// outcome came through; its Source is the Channel.
const ReportedOutcomeKind = "reported_outcome"

// normal is c when TokenOps knows it, and otherwise ViaAgent: a channel
// nobody vouches for is the weakest.
func (c Channel) normal() Channel {
	switch c {
	case ViaCLI, ViaAPI, ViaOperatorConfirmed, ViaAgent:
		return c
	default:
		return ViaAgent
	}
}

// Assessment is what an outcome through c can vouch for.
func (c Channel) Assessment() eventschema.OutcomeAssessment {
	if c.normal() == ViaAgent {
		return eventschema.OutcomeSelfReport
	}
	return eventschema.OutcomeHuman
}

// Record describes an outcome observation without raw command output.
type Record struct {
	ExecutionID    string
	DecisionID     string
	InterventionID string
	Result         eventschema.OutcomeResult
	// Assessment is the evidence's strength. A record with Via set takes
	// its channel's instead.
	Assessment eventschema.OutcomeAssessment
	// Via is the channel a reported outcome came through; empty for an
	// observed one (a verifier result).
	Via              Channel
	Caveat           string
	At               time.Time
	Evidence         []eventschema.EvidenceRef
	AttentionMinutes *float64
}

// Event builds the canonical outcome envelope.
func Event(r Record) *eventschema.Envelope {
	at := r.At.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	evidence := r.Evidence
	if r.Via != "" {
		via := r.Via.normal()
		r.Assessment = via.Assessment()
		evidence = append(append([]eventschema.EvidenceRef(nil), evidence...), eventschema.EvidenceRef{
			Kind: ReportedOutcomeKind, Source: string(via), ObservedAt: at,
		})
	}
	var metrics []eventschema.OutcomeMetric
	// Human attention is the operator's own figure; an agent's estimate of
	// it is not kept.
	if r.AttentionMinutes != nil && r.Assessment == eventschema.OutcomeHuman && !math.IsNaN(*r.AttentionMinutes) && !math.IsInf(*r.AttentionMinutes, 0) && *r.AttentionMinutes >= 0 {
		metrics = append(metrics, eventschema.OutcomeMetric{
			Name: "human_attention_minutes", Unit: "minutes", Value: *r.AttentionMinutes,
			Source: "human_self_report", ObservedAt: at, Confidence: 1,
		})
	}
	return &eventschema.Envelope{
		ID: uuid.NewString(), SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypeOutcome, Timestamp: at, Source: "outcome",
		Association: eventschema.Association{Execution: r.ExecutionID},
		Correlation: eventschema.Correlation{
			Decision: r.DecisionID, Intervention: r.InterventionID,
		},
		Payload: &eventschema.OutcomeEvent{
			Result: r.Result, Assessment: r.Assessment, Caveat: r.Caveat, Evidence: evidence, Metrics: metrics,
		},
	}
}

// CorrelateDecisionLifecycle copies intervention and experiment identity from
// the decision history. Outcome callers use this before persistence so later
// verification can join by durable lifecycle IDs without reconstructing them.
func CorrelateDecisionLifecycle(env *eventschema.Envelope, history []*eventschema.Envelope) {
	if env == nil || env.Correlation.Decision == "" {
		return
	}
	for _, candidate := range history {
		if candidate == nil || candidate.Correlation.Decision != env.Correlation.Decision {
			continue
		}
		if candidate.Correlation.Intervention != "" {
			env.Correlation.Intervention = candidate.Correlation.Intervention
		}
		if candidate.Correlation.Experiment != "" {
			env.Correlation.Experiment = candidate.Correlation.Experiment
		}
	}
}

// FromToolEvents uses the last completed verifier after the final edit. Tool
// output and command bodies are deliberately not copied into the event.
func FromToolEvents(executionID, decisionID string, events []toolcoach.ToolEvent) (*eventschema.Envelope, bool) {
	uses := map[string]toolcoach.ToolEvent{}
	results := map[string]toolcoach.ToolEvent{}
	var lastEdit time.Time
	for _, ev := range events {
		if ev.IsResult {
			results[ev.ToolUseID] = ev
			continue
		}
		uses[ev.ToolUseID] = ev
		if isEdit(ev.Name) && ev.Timestamp.After(lastEdit) {
			lastEdit = ev.Timestamp
		}
	}
	type completed struct {
		use, result toolcoach.ToolEvent
	}
	var candidates []completed
	for id, use := range uses {
		result, ok := results[id]
		if !ok || !isVerifier(use) || result.Timestamp.Before(lastEdit) {
			continue
		}
		candidates = append(candidates, completed{use: use, result: result})
	}
	if len(candidates) == 0 {
		return nil, false
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].result.Timestamp.Before(candidates[j].result.Timestamp) })
	last := candidates[len(candidates)-1]
	result := eventschema.OutcomeAchieved
	caveat := "the final verifier after the last edit passed; this establishes only the verifier's scope"
	if last.result.IsError {
		result = eventschema.OutcomeNotAchieved
		caveat = "the final verifier after the last edit failed"
	}
	return Event(Record{
		ExecutionID: executionID, DecisionID: decisionID, Result: result,
		Assessment: eventschema.OutcomeVerification, Caveat: caveat, At: last.result.Timestamp,
		Evidence: []eventschema.EvidenceRef{{
			Kind: "command_verification", Source: "local_transcript", ObservedAt: last.result.Timestamp,
			Confidence: 1, Scope: verifierKind(last.use.RawCommand),
		}},
	}), true
}

// DetectSession extracts a local Claude Code session and evaluates only its
// final recognized verifier after the final edit.
func DetectSession(executionID, decisionID, sessionID string) (*eventschema.Envelope, bool, error) {
	events, err := toolcoach.Extract(toolcoach.ExtractOptions{SessionID: sessionID})
	if err != nil {
		return nil, false, err
	}
	env, ok := FromToolEvents(executionID, decisionID, events)
	return env, ok, nil
}

// Resolve selects the strongest assessment. Conflicting assessments at the
// same strength become partial and must not be used as clean learning signal.
func Resolve(events []*eventschema.Envelope) eventschema.OutcomeEvent {
	var strongest int
	selected := make([]eventschema.OutcomeEvent, 0, 2)
	for _, env := range events {
		if env == nil {
			continue
		}
		out, ok := env.Payload.(*eventschema.OutcomeEvent)
		if !ok {
			continue
		}
		s := strength(out.Assessment)
		switch {
		case s > strongest:
			strongest, selected = s, append(selected[:0], *out)
		case s == strongest:
			selected = append(selected, *out)
		}
	}
	if len(selected) == 0 {
		return eventschema.OutcomeEvent{Result: eventschema.OutcomeUnknown, Caveat: "nothing assessed this execution"}
	}
	result := selected[len(selected)-1]
	for _, other := range selected[:len(selected)-1] {
		if other.Result != result.Result {
			return eventschema.OutcomeEvent{
				Result: eventschema.OutcomePartial, Assessment: result.Assessment,
				Caveat: "conflicting assessments of equal strength",
			}
		}
	}
	return result
}

func strength(a eventschema.OutcomeAssessment) int {
	switch a {
	case eventschema.OutcomeHuman:
		return 3
	case eventschema.OutcomeVerification:
		return 2
	case eventschema.OutcomeSelfReport:
		return 1
	default:
		return 0
	}
}

func isEdit(name string) bool {
	switch strings.ToLower(name) {
	case "edit", "write", "multiedit", "notebookedit", "apply_patch", "patch":
		return true
	default:
		return false
	}
}

func isVerifier(ev toolcoach.ToolEvent) bool {
	if !strings.EqualFold(ev.Name, "bash") {
		return false
	}
	return verifierKind(ev.RawCommand) != ""
}

func verifierKind(command string) string {
	c := strings.ToLower(strings.TrimSpace(command))
	for _, candidate := range []struct{ prefix, kind string }{
		{"go test", "go_test"}, {"go vet", "go_vet"}, {"golangci-lint", "golangci_lint"},
		{"pytest", "pytest"}, {"python -m pytest", "pytest"}, {"npm test", "npm_test"},
		{"npm run test", "npm_test"}, {"pnpm test", "pnpm_test"}, {"cargo test", "cargo_test"},
		{"make test", "make_test"}, {"make verify", "make_verify"},
	} {
		if strings.HasPrefix(c, candidate.prefix) {
			return candidate.kind
		}
	}
	return ""
}
