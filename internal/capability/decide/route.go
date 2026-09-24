// Package decide turns a domain recommendation into a durable, explainable
// control-plane decision. It deliberately never stores the instruction that
// produced the recommendation.
package decide

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router"
	"go.klarlabs.de/tokenops/internal/contexts/policy"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// RoutingAuthority resolves the effective ladder behind legacy configuration
// vocabularies so adapters never interpret policy independently.
func RoutingAuthority(cfg config.Config) policy.Authority {
	return policy.Effective(
		policy.FromDaemonMode(cfg.Mode),
		policy.FromSmartRoutingIntervention(cfg.Optimizer.SmartRouting.Intervention),
	)
}

// AutomaticAuthority is used by an execution adapter after its composition
// root has already established automatic authority.
func AutomaticAuthority() policy.Authority { return policy.Automatic }

// Adapter declares what the calling surface can actually do.
type Adapter struct {
	Name          string
	CanApplyRoute bool
}

// RouteInput contains derived decision inputs only. Raw instructions must not
// cross this boundary because the returned event is persisted.
type RouteInput struct {
	ID           string
	At           time.Time
	Provider     eventschema.Provider
	CurrentModel string
	Advice       router.Advice
	Authority    policy.Authority
	Adapter      Adapter
	Association  eventschema.Association
	WindowKnown  bool
	WindowPct    float64
	ObservedAt   time.Time
}

// RouteResult is the structural insight rendered by each surface.
type RouteResult struct {
	DecisionID string
	Stage      eventschema.DecisionStage
	Executable bool
	Event      *eventschema.Envelope
}

// Route records what was considered, what was selected, and why.
func Route(in RouteInput) RouteResult {
	at := in.At.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	id := in.ID
	if id == "" {
		id = "decision:" + uuid.NewString()
	}
	selected := in.CurrentModel
	changed := !in.Advice.Stay && in.Advice.Model != "" && in.Advice.Model != in.CurrentModel
	if changed {
		selected = in.Advice.Model
	}

	stage := eventschema.DecisionStageShadow
	if in.Authority.MaySpeak() && changed {
		stage = eventschema.DecisionStageProposed
	}
	executable := changed && in.Authority.MayAct() && in.Adapter.CanApplyRoute
	if executable && !in.Authority.NeedsApproval() {
		stage = eventschema.DecisionStageApplied
	}

	evidence := []eventschema.EvidenceRef{{
		Kind: "task_classification", Source: "local_router", ObservedAt: at,
		Confidence: in.Advice.Quality, Scope: in.Advice.Class,
	}}
	if in.WindowKnown {
		observed := in.ObservedAt.UTC()
		if observed.IsZero() {
			observed = at
		}
		evidence = append(evidence, eventschema.EvidenceRef{
			Kind: "capacity_pressure", Source: "local_event_store", ObservedAt: observed,
			FreshUntil: observed.Add(15 * time.Minute), Confidence: 1,
			Scope: string(in.Provider), Caveat: percentCaveat(in.WindowPct),
		})
	}

	interventionID := ""
	if changed {
		interventionID = "intervention:" + strings.TrimPrefix(id, "decision:")
	}
	payload := &eventschema.DecisionEvent{
		Kind: "model_route", Stage: stage,
		Alternatives: []eventschema.ResourceOption{
			{Provider: string(in.Provider), Model: in.CurrentModel},
			{Provider: string(in.Provider), Model: in.Advice.Model},
		},
		Selected: eventschema.ResourceOption{Provider: string(in.Provider), Model: selected},
		Evidence: evidence, Policy: "quality_first_capacity_second",
		Authority: in.Authority.String(), Confidence: in.Advice.Quality,
		Rationale: in.Advice.Reason, Executable: executable,
		Adapter: in.Adapter.Name, Fingerprint: RouteFingerprint(in.Provider, in.CurrentModel, selected, in.Adapter.Name),
	}
	return RouteResult{
		DecisionID: id, Stage: stage, Executable: executable,
		Event: &eventschema.Envelope{
			ID: uuid.NewString(), SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypeDecision, Timestamp: at, Source: "decision",
			Association: in.Association,
			Correlation: eventschema.Correlation{Decision: id, Intervention: interventionID},
			Payload:     payload,
		},
	}
}

// RouteFingerprint invalidates learned behavior when its resource path or
// execution adapter changes.
func RouteFingerprint(provider eventschema.Provider, current, selected, adapter string) string {
	sum := sha256.Sum256([]byte(string(provider) + "\x00" + current + "\x00" + selected + "\x00" + adapter))
	return "sha256:" + hex.EncodeToString(sum[:8])
}

func percentCaveat(pct float64) string {
	// Keep the measured amount explainable without creating a second
	// unproven numeric field on the decision wire contract.
	return fmt.Sprintf("window pressure was %.1f%% before this decision", pct)
}
