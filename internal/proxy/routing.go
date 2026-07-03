package proxy

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

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
		}
		recs, err := s.router.Run(r.Context(), req)
		if err != nil || len(recs) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		rec := recs[0]
		if rec.ApplyBody == nil {
			// Passive recommendation (no available target / unparseable
			// body) — forward unchanged but still record the event.
			s.publishRoutingEvent(obs, rec, eventschema.OptimizationDecisionSkipped)
			next.ServeHTTP(w, r)
			return
		}

		r.Body = io.NopCloser(bytes.NewReader(rec.ApplyBody))
		r.ContentLength = int64(len(rec.ApplyBody))
		r.Header.Set("Content-Length", strconv.Itoa(len(rec.ApplyBody)))
		s.logger.Info("active routing applied",
			"provider", provider.ID,
			"reason", rec.Reason,
			"estimated_savings_usd", rec.EstimatedSavingsUSD,
			"workflow_id", obs.WorkflowID,
		)
		s.publishRoutingEvent(obs, rec, eventschema.OptimizationDecisionApplied)
		next.ServeHTTP(w, r)
	})
}

// publishRoutingEvent records the routing decision on the event bus so
// the intervention is auditable next to the PromptEvent it altered.
func (s *Server) publishRoutingEvent(obs *requestObservation, rec optimizer.Recommendation, decision eventschema.OptimizationDecision) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(&eventschema.Envelope{
		ID:            uuid.NewString(),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypeOptimization,
		Timestamp:     time.Now().UTC(),
		Source:        s.source,
		Payload: &eventschema.OptimizationEvent{
			PromptHash:             obs.PromptHash,
			Kind:                   rec.Kind,
			Mode:                   eventschema.OptimizationModeInteractive,
			EstimatedSavingsTokens: rec.EstimatedSavingsTokens,
			EstimatedSavingsUSD:    rec.EstimatedSavingsUSD,
			QualityScore:           rec.QualityScore,
			Decision:               decision,
			Reason:                 rec.Reason,
			WorkflowID:             obs.WorkflowID,
			AgentID:                obs.AgentID,
		},
	})
}
