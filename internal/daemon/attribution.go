package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/contexts/spend/biller"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/opencode"
	"go.klarlabs.de/tokenops/internal/infra/claudesettings"
	"go.klarlabs.de/tokenops/internal/infra/routehistory"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// correctGatewayAttribution corrects Claude Code turns recorded before
// TokenOps knew which endpoint they went through (ADR 0009). Every turn
// used to be recorded as Anthropic's and, on a plan, as covered by it.
// Walking the route history stretch by stretch:
//
//   - a turn served by a model Anthropic does not serve moves to the
//     biller the endpoint (or the model's namespace) names, and is no
//     longer covered by the Anthropic plan;
//   - a Claude turn through a gateway records that endpoint and becomes
//     billed, since it ran on an API key, not the plan.
//
// The correction is TokenOps' own, so it runs without asking, is
// audited, and changes nothing on a second pass.
func correctGatewayAttribution(ctx context.Context, cfg config.Config, store *sqlite.Store, routes *routehistory.Tracker, logger *slog.Logger) {
	if store == nil {
		return
	}
	now := time.Now().UTC()
	var stretches []biller.Stretch
	if routes != nil {
		stretches = routes.Routes().Stretches(routehistory.HarnessClaudeCode, now.Add(time.Hour))
	}
	if len(stretches) == 0 {
		stretches = []biller.Stretch{{BaseURL: claudesettings.BaseURL(), To: now.Add(time.Hour)}}
	}
	anthropic := string(eventschema.ProviderAnthropic)
	audited := func(details map[string]any, to string) {
		_, _ = audit.NewRecorder(store).Record(ctx, audit.Entry{
			Action: audit.ActionCostCorrection, Actor: "tokenops", Target: to, Details: details,
		})
	}
	for _, st := range stretches {
		endpoint := biller.EndpointName(st.BaseURL, anthropic)
		models, err := store.ModelsFor(ctx, claudecodejsonl.SourceTag, anthropic)
		if err != nil {
			logger.Warn("gateway attribution check failed; will retry at next start", "err", err)
			return
		}
		for _, model := range models {
			to := string(biller.ForClaudeCodeTurn(model, st.BaseURL))
			if to == anthropic {
				continue
			}
			n, err := store.Reattribute(ctx, claudecodejsonl.SourceTag, model, anthropic, to,
				biller.EndpointName(st.BaseURL, to), st.From, st.To, !cfg.PlanCovers(to))
			if err != nil {
				logger.Warn("gateway attribution failed; will retry at next start", "model", model, "err", err)
				return
			}
			if n > 0 {
				logger.Info("re-attributed Claude Code turns a gateway served", "model", model, "to", to, "calls", n)
				audited(map[string]any{
					"model": model, "from": anthropic, "to": to, "calls": n,
					"reason": "Claude Code turns served by a model Anthropic does not serve were recorded as Anthropic's",
				}, to)
			}
		}
		if endpoint == anthropic {
			continue
		}
		n, err := store.MarkEndpoint(ctx, claudecodejsonl.SourceTag, anthropic, endpoint, st.From, st.To)
		if err != nil {
			logger.Warn("gateway endpoint correction failed; will retry at next start", "err", err)
			return
		}
		if n > 0 {
			logger.Info("marked Claude turns that went through a gateway", "endpoint", endpoint, "calls", n)
			audited(map[string]any{
				"endpoint": endpoint, "calls": n, "from": st.From.Format(time.RFC3339), "to": st.To.Format(time.RFC3339),
				"reason": "Claude turns through a gateway run on an API key, not the plan, and were recorded as covered",
			}, anthropic)
		}
	}
}

// correctOpencodeAttribution renames opencode turns stored under a raw
// providerID that now maps to a TokenOps provider ("zai-coding-plan" is
// z.ai's, through its plan endpoint). Idempotent and audited.
func correctOpencodeAttribution(ctx context.Context, store *sqlite.Store, logger *slog.Logger) {
	if store == nil {
		return
	}
	stored, err := store.ProvidersFor(ctx, opencode.SourceTag)
	if err != nil {
		logger.Warn("opencode attribution check failed; will retry at next start", "err", err)
		return
	}
	for _, from := range stored {
		to, endpoint, ok := opencode.Provider(from)
		if !ok || string(to) == from {
			continue
		}
		n, err := store.RenameProvider(ctx, opencode.SourceTag, from, string(to), endpoint)
		if err != nil {
			logger.Warn("opencode attribution failed; will retry at next start", "err", err)
			return
		}
		if n == 0 {
			continue
		}
		logger.Info("re-attributed opencode turns", "from", from, "to", to, "calls", n)
		_, _ = audit.NewRecorder(store).Record(ctx, audit.Entry{
			Action: audit.ActionCostCorrection, Actor: "tokenops", Target: string(to),
			Details: map[string]any{
				"from": from, "to": string(to), "endpoint": endpoint, "calls": n,
				"reason": "opencode turns were recorded under the client's provider ID, not the provider that bills them",
			},
		})
	}
}
