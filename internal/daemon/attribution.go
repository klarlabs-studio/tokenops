package daemon

import (
	"context"
	"log/slog"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/contexts/spend/biller"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	"go.klarlabs.de/tokenops/internal/infra/claudesettings"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// correctGatewayAttribution moves Claude Code turns recorded as
// Anthropic's, for a model Anthropic does not serve, to the biller that
// served them (ADR 0009). Before biller.ForClaudeCodeTurn, every Claude
// Code turn was recorded as Anthropic's, so a gateway's turns (kimi-k3
// through Fireworks) counted against the Anthropic plan or spend limit,
// with no price. The correction is TokenOps' own, so it runs without
// asking, is audited, and changes nothing on a second pass.
//
// Past turns are judged by the endpoint Claude Code is pointed at now and
// by the model's own namespace. A model no rule places becomes "unknown",
// which is still truer than Anthropic for a model Anthropic does not serve.
func correctGatewayAttribution(ctx context.Context, cfg config.Config, store *sqlite.Store, logger *slog.Logger) {
	if store == nil {
		return
	}
	anthropic := string(eventschema.ProviderAnthropic)
	models, err := store.ModelsFor(ctx, claudecodejsonl.SourceTag, anthropic)
	if err != nil {
		logger.Warn("gateway attribution check failed; will retry at next start", "err", err)
		return
	}
	base := claudesettings.BaseURL()
	for _, model := range models {
		to := biller.ForClaudeCodeTurn(model, base)
		if to == eventschema.ProviderAnthropic {
			continue
		}
		n, err := store.Reattribute(ctx, claudecodejsonl.SourceTag, model, anthropic, string(to), !cfg.PlanCovers(string(to)))
		if err != nil {
			logger.Warn("gateway attribution failed; will retry at next start", "model", model, "err", err)
			return
		}
		if n == 0 {
			continue
		}
		logger.Info("re-attributed Claude Code turns a gateway served", "model", model, "to", to, "calls", n)
		_, _ = audit.NewRecorder(store).Record(ctx, audit.Entry{
			Action: audit.ActionCostCorrection, Actor: "tokenops", Target: string(to),
			Details: map[string]any{
				"model": model, "from": anthropic, "to": string(to), "calls": n,
				"reason": "Claude Code turns served by a model Anthropic does not serve were recorded as Anthropic's",
			},
		})
	}
}
