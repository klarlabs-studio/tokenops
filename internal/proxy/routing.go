package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/tokenops/internal/capability/decide"
	"go.klarlabs.de/tokenops/internal/capability/experiments"
	"go.klarlabs.de/tokenops/internal/capability/learn"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router"
	"go.klarlabs.de/tokenops/internal/contexts/prompts/providers"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// WithActiveRouting enables live model routing (active mode): requests
// matching a configured routing rule are rewritten to the cheaper
// target model before they reach the upstream. Every applied route is
// recorded as an OptimizationEvent (mode=interactive, decision=applied)
// on the event bus so dashboards and replay can audit interventions.
// spendEng may be nil — savings then surface as token counts only.
func WithActiveRouting(cfg router.Config, spendEng *spend.Engine) Option {
	return func(s *Server) {
		if len(cfg.Rules) == 0 {
			return
		}
		s.router = router.New(cfg, spendEng)
	}
}

// routingMiddleware applies the model router to the request body. It
// runs inside the observer middleware so the observation keeps the
// original requested model — the intervention is visible as
// RequestModel ≠ ResponseModel plus the emitted OptimizationEvent.
//
// Failure stance: routing must never break the request path. Any parse
// or rewrite problem forwards the original body untouched.
func (s *Server) routingMiddleware(provider providers.Provider, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		obs, _ := r.Context().Value(observationKey{}).(*requestObservation)
		if s.router == nil || obs == nil || obs.RequestModel == "" {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodyCapture+1))
		_ = r.Body.Close()
		if err != nil || int64(len(body)) > maxRequestBodyCapture {
			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		req := &optimizer.Request{
			PromptHash:   obs.PromptHash,
			Provider:     provider.ID,
			Model:        obs.RequestModel,
			WorkflowID:   obs.WorkflowID,
			AgentID:      obs.AgentID,
			InputTokens:  obs.InputTokens,
			OutputTokens: obs.MaxOutput,
			Body:         body,
			Mode:         optimizer.ModeInteractive,
			CostSource:   obs.CostSource,
		}
		recs, err := s.router.Run(r.Context(), req)
		if err != nil || len(recs) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		rec := recs[0]
		var trialRec *optimizer.Recommendation
		if rec.ApplyBody == nil && strings.HasPrefix(rec.Reason, "observed:") &&
			rec.TargetModel != "" && obs.ExecutionID != "" && s.experiments != nil {
			// Observe-only remains the normal policy. Build an applied candidate
			// only so an explicit, execution-linked experiment can select it.
			// The candidate is never used unless Assign confirms enrollment.
			trialRecs, trialErr := s.router.RunForExplicitTrial(r.Context(), req)
			if trialErr == nil && len(trialRecs) > 0 &&
				trialRecs[0].TargetModel == rec.TargetModel && len(trialRecs[0].ApplyBody) > 0 {
				trialRec = &trialRecs[0]
			}
		}
		if reason := anthropicRouteIncompatibility(provider.ID, rec.TargetModel, body); reason != "" {
			rec.ApplyBody = nil
			rec.Reason = reason + "; preserving the baseline model"
			s.publishRoutingEvent(obs, rec, eventschema.OptimizationDecisionSkipped)
			next.ServeHTTP(w, r)
			return
		}

		var assignment experiments.Assignment
		var enrolled bool
		if rec.ApplyBody != nil || trialRec != nil {
			var assignErr error
			assignment, enrolled, assignErr = s.experiments.Assign(r.Context(), experiments.AssignmentInput{
				Provider: string(provider.ID), BaselineModel: obs.RequestModel, VariantModel: rec.TargetModel,
				ExecutionID: obs.ExecutionID,
				Fingerprint: decide.RouteFingerprint(provider.ID, obs.RequestModel, rec.TargetModel, "proxy"),
				At:          time.Now().UTC(),
			})
			if assignErr != nil {
				s.logger.Warn("routing experiment assignment failed; preserving baseline", "err", assignErr)
				next.ServeHTTP(w, r)
				return
			}
		}
		if enrolled && !assignment.Variant {
			controlDecision := decide.Route(decide.RouteInput{
				Provider: provider.ID, CurrentModel: obs.RequestModel,
				Advice:      router.Advice{Model: rec.TargetModel, Reason: rec.Reason, Quality: rec.QualityScore},
				Adapter:     decide.Adapter{Name: "proxy", CanApplyRoute: true},
				Association: associationFor(obs),
			})
			controlDecision.Event.Correlation.Experiment = assignment.ExperimentID
			controlDecision.Event.Attributes = experimentAttributes(assignment, false)
			obs.Correlation = controlDecision.Event.Correlation
			s.publishRoutingEvent(obs, rec, eventschema.OptimizationDecisionSkipped, controlDecision)
			next.ServeHTTP(w, r)
			return
		}
		if enrolled && assignment.Variant && rec.ApplyBody == nil && trialRec != nil {
			rec = *trialRec
		}
		if rec.ApplyBody == nil {
			// Passive recommendation (no available target / unparseable
			// body) — forward unchanged but still record the event.
			s.publishRoutingEvent(obs, rec, eventschema.OptimizationDecisionSkipped)
			next.ServeHTTP(w, r)
			return
		}

		fingerprint := decide.RouteFingerprint(provider.ID, obs.RequestModel, rec.TargetModel, "proxy")
		var belief *learn.Belief
		if !enrolled {
			if s.experiments == nil {
				s.preserveUntrustedRoute(obs, provider.ID, rec, nil, "outcome history unavailable; preserving the baseline model")
				next.ServeHTTP(w, r)
				return
			}
			learned, found, learnErr := learn.FindRouting(r.Context(), s.experiments, fingerprint, time.Now().UTC())
			if learnErr != nil {
				s.logger.Warn("routing belief lookup failed; preserving baseline", "err", learnErr)
				s.preserveUntrustedRoute(obs, provider.ID, rec, nil, "outcome history could not be read; preserving the baseline model")
				next.ServeHTTP(w, r)
				return
			}
			if found {
				belief = &learned
			}
			if !found || !learn.IsTrusted(learned) {
				reason := "no trusted outcome-linked evidence for this route; preserving the baseline model"
				if found {
					reason = "route evidence tier is " + string(learned.Tier) + "; preserving the baseline model"
				}
				s.preserveUntrustedRoute(obs, provider.ID, rec, belief, reason)
				next.ServeHTTP(w, r)
				return
			}
		}

		r.Body = io.NopCloser(bytes.NewReader(rec.ApplyBody))
		obs.RoutedModel = rec.TargetModel
		r.ContentLength = int64(len(rec.ApplyBody))
		r.Header.Set("Content-Length", strconv.Itoa(len(rec.ApplyBody)))
		s.logger.Info("active routing applied",
			"provider", provider.ID,
			"reason", rec.Reason,
			"estimated_savings_usd", rec.EstimatedSavingsUSD,
			"workflow_id", obs.WorkflowID,
		)
		controlDecision := decide.Route(decide.RouteInput{
			Provider: provider.ID, CurrentModel: obs.RequestModel,
			Advice:      router.Advice{Model: rec.TargetModel, Reason: rec.Reason, Quality: rec.QualityScore},
			Authority:   decide.AutomaticAuthority(),
			Adapter:     decide.Adapter{Name: "proxy", CanApplyRoute: true},
			Association: associationFor(obs),
			Belief:      belief,
		})
		if enrolled {
			controlDecision.Event.Correlation.Experiment = assignment.ExperimentID
			controlDecision.Event.Attributes = experimentAttributes(assignment, true)
		}
		obs.Correlation = controlDecision.Event.Correlation
		s.publishRoutingEvent(obs, rec, eventschema.OptimizationDecisionApplied, controlDecision)
		next.ServeHTTP(w, r)
	})
}

// anthropicRouteIncompatibility rejects model-only rewrites when the inbound
// request uses a feature the target model does not accept. Reinterpreting a
// system instruction, thinking policy, sampling policy, or assistant prefill
// would change request semantics, so the safe action is to keep the baseline.
func anthropicRouteIncompatibility(provider eventschema.Provider, target string, body []byte) string {
	if provider != eventschema.ProviderAnthropic || !strings.HasPrefix(target, "claude-sonnet-5") {
		return ""
	}

	manualThinking, samplingParameter := anthropicCompatibilityTraits(body)
	if manualThinking {
		return "target claude-sonnet-5 does not support manual extended thinking"
	}
	if samplingParameter {
		return "target claude-sonnet-5 does not support explicit sampling parameters"
	}

	var request struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &request) != nil {
		return ""
	}
	for _, message := range request.Messages {
		if message.Role == "system" {
			return "target claude-sonnet-5 does not support mid-conversation system messages"
		}
	}
	if len(request.Messages) > 0 && request.Messages[len(request.Messages)-1].Role == "assistant" {
		return "target claude-sonnet-5 does not support assistant response prefills"
	}
	return ""
}

func (s *Server) preserveUntrustedRoute(obs *requestObservation, provider eventschema.Provider, rec optimizer.Recommendation, belief *learn.Belief, reason string) {
	controlDecision := decide.Route(decide.RouteInput{
		Provider: provider, CurrentModel: obs.RequestModel,
		Advice:    router.Advice{Model: rec.TargetModel, Reason: reason + ": " + rec.Reason, Quality: rec.QualityScore},
		Authority: decide.RecommendAuthority(), Adapter: decide.Adapter{Name: "proxy", CanApplyRoute: true},
		Association: associationFor(obs), Belief: belief,
	})
	obs.Correlation = controlDecision.Event.Correlation
	s.publishRoutingEvent(obs, rec, eventschema.OptimizationDecisionSkipped, controlDecision)
}

func experimentAttributes(a experiments.Assignment, variant bool) map[string]string {
	arm := "baseline"
	if variant {
		arm = "variant"
	}
	return map[string]string{
		"tokenops.experiment.pair": strconv.Itoa(a.Pair),
		"tokenops.experiment.arm":  arm,
	}
}

// publishRoutingEvent records the routing decision on the event bus so
// the intervention is auditable next to the PromptEvent it altered.
func (s *Server) publishRoutingEvent(obs *requestObservation, rec optimizer.Recommendation, decision eventschema.OptimizationDecision, control ...decide.RouteResult) {
	if s.bus == nil {
		return
	}
	correlation := eventschema.Correlation{}
	reason := rec.Reason
	if len(control) > 0 {
		correlation = control[0].Event.Correlation
		if recorded, ok := control[0].Event.Payload.(*eventschema.DecisionEvent); ok && recorded.Rationale != "" {
			reason = recorded.Rationale
		}
		s.bus.Publish(control[0].Event)
	}
	s.bus.Publish(&eventschema.Envelope{
		ID:            uuid.NewString(),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypeOptimization,
		Timestamp:     time.Now().UTC(),
		Source:        s.source,
		Association:   associationFor(obs),
		Correlation:   correlation,
		Payload: &eventschema.OptimizationEvent{
			PromptHash:             obs.PromptHash,
			Kind:                   rec.Kind,
			Mode:                   eventschema.OptimizationModeInteractive,
			EstimatedSavingsTokens: rec.EstimatedSavingsTokens,
			EstimatedSavingsUSD:    rec.EstimatedSavingsUSD,
			QualityScore:           rec.QualityScore,
			Decision:               decision,
			Reason:                 reason,
			WorkflowID:             obs.WorkflowID,
			AgentID:                obs.AgentID,
		},
	})
}
