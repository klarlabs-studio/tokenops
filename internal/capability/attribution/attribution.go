// Package attribution corrects which provider bills usage recorded before
// TokenOps knew the endpoint it went through (ADR 0009), and answers
// whether a plan covers a turn at all.
//
// The corrections are TokenOps' own: they run at daemon start without
// asking, each one is audited, and a second pass changes nothing.
package attribution

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/contexts/spend/biller"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexjsonl"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/opencode"
	"go.klarlabs.de/tokenops/internal/infra/claudesettings"
	"go.klarlabs.de/tokenops/internal/infra/codexsettings"
	"go.klarlabs.de/tokenops/internal/infra/routehistory"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Correction is one move of recorded usage to the provider that bills it.
type Correction struct {
	// Harness is the client whose turns moved: claude-code, codex or
	// opencode.
	Harness string
	// From and To are the providers the turns moved between; Model is
	// set when only one model's turns moved.
	From, To, Model string
	// Endpoint is the endpoint the turns are now recorded as going
	// through, when the correction marked one.
	Endpoint string
	// Calls is how many turns moved.
	Calls int64
	// Reason says why, as the audit log records it.
	Reason string
}

// PlanApplies reports whether a plan for provider covers a turn that went
// through endpoint: a plan covers only its vendor's own endpoint, and a
// turn a gateway carried runs on an API key, billed per token.
func PlanApplies(provider, endpoint string) bool { return biller.PlanApplies(provider, endpoint) }

// record writes a correction to the audit log.
func record(ctx context.Context, store *sqlite.Store, target string, details map[string]any) {
	_, _ = audit.NewRecorder(store).Record(ctx, audit.Entry{
		Action: audit.ActionCostCorrection, Actor: "tokenops", Target: target, Details: details,
	})
}

// CorrectGateway corrects Claude Code turns recorded as Anthropic's and,
// on a plan, as covered by it. Walking the route history stretch by
// stretch:
//
//   - a turn served by a model Anthropic does not serve moves to the
//     biller the endpoint (or the model's namespace) names, and is no
//     longer covered by the Anthropic plan;
//   - a Claude turn through a gateway records that endpoint and becomes
//     billed, since it ran on an API key, not the plan.
//
// With no route history, Claude Code's base URL as it is now stands for
// all of it. It returns the corrections made before any error.
func CorrectGateway(ctx context.Context, cfg config.Config, store *sqlite.Store, routes *routehistory.Tracker, now time.Time) ([]Correction, error) {
	if store == nil {
		return nil, nil
	}
	var stretches []biller.Stretch
	if routes != nil {
		stretches = routes.Routes().Stretches(routehistory.HarnessClaudeCode, now.Add(time.Hour))
	}
	if len(stretches) == 0 {
		stretches = []biller.Stretch{{BaseURL: claudesettings.BaseURL(), To: now.Add(time.Hour)}}
	}
	anthropic := string(eventschema.ProviderAnthropic)
	models, err := store.ModelsFor(ctx, claudecodejsonl.SourceTag, anthropic)
	if err != nil {
		return nil, err
	}
	var out []Correction
	for _, st := range stretches {
		for _, model := range models {
			to := string(biller.ForClaudeCodeTurn(model, st.BaseURL))
			if to == anthropic {
				continue
			}
			endpoint := biller.EndpointName(st.BaseURL, to)
			n, err := store.Reattribute(ctx, claudecodejsonl.SourceTag, model, anthropic, to, endpoint, st.From, st.To, !cfg.PlanCovers(to))
			if err != nil {
				return out, err
			}
			if n == 0 {
				continue
			}
			c := Correction{Harness: "claude-code", From: anthropic, To: to, Model: model, Endpoint: endpoint, Calls: n,
				Reason: "Claude Code turns served by a model Anthropic does not serve were recorded as Anthropic's"}
			record(ctx, store, to, map[string]any{"model": model, "from": anthropic, "to": to, "calls": n, "reason": c.Reason})
			out = append(out, c)
		}
		endpoint := biller.EndpointName(st.BaseURL, anthropic)
		if endpoint == anthropic {
			continue
		}
		n, err := store.MarkEndpoint(ctx, claudecodejsonl.SourceTag, anthropic, endpoint, st.From, st.To)
		if err != nil {
			return out, err
		}
		if n == 0 {
			continue
		}
		c := Correction{Harness: "claude-code", From: anthropic, To: anthropic, Endpoint: endpoint, Calls: n,
			Reason: "Claude turns through a gateway run on an API key, not the plan, and were recorded as covered"}
		record(ctx, store, anthropic, map[string]any{
			"endpoint": endpoint, "calls": n, "from": st.From.Format(time.RFC3339), "to": st.To.Format(time.RFC3339), "reason": c.Reason,
		})
		out = append(out, c)
	}
	return out, nil
}

// CorrectCodex moves Codex turns recorded as OpenAI's to the provider
// their session actually used, and marks the endpoint they went through.
// A session on a custom model_provider (Fireworks, z.ai, a company
// gateway) names it in its rollout's first line, and Codex's config gives
// its base URL as it is now.
//
// Each turn ends where live ingestion records it, decided per model: a
// gateway running OpenAI's models on the operator's own credential bills
// its own models, and leaves OpenAI's with OpenAI, billed per token on the
// key the gateway passes on rather than covered by the ChatGPT plan.
//
// This correction once decided per session and moved those OpenAI turns
// to the gateway at every start. It now also moves them back: the turns
// it looks at are the session's under OpenAI and under the gateway.
func CorrectCodex(ctx context.Context, cfg config.Config, store *sqlite.Store) ([]Correction, error) {
	if store == nil || !cfg.VendorUsage.CodexJSONL.Enabled {
		return nil, nil
	}
	root := cfg.VendorUsage.CodexJSONL.Root
	if root == "" {
		r, err := codexjsonl.DefaultRoot()
		if err != nil {
			return nil, nil
		}
		root = r
	}
	sessions, err := codexjsonl.SessionProviders(root)
	if err != nil {
		return nil, err
	}
	openai := string(eventschema.ProviderOpenAI)
	type move struct{ from, to string }
	moved := map[move]int64{}
	for session, id := range sessions {
		base := codexsettings.ProviderBaseURL(id)
		endpoint := biller.EndpointName(base, id)
		recordedAs := []string{openai}
		if gw := string(biller.ForCodexTurn(id, base, "")); gw != openai {
			recordedAs = append(recordedAs, gw)
		}
		for _, from := range recordedAs {
			models, err := store.SessionModels(ctx, codexjsonl.SourceTag, from, session)
			if err != nil {
				return nil, err
			}
			for _, model := range models {
				to := string(biller.ForCodexTurn(id, base, model))
				uncover := !cfg.PlanCovers(to) || !biller.PlanApplies(to, endpoint)
				n, err := store.ReattributeSession(ctx, codexjsonl.SourceTag, session, model, from, to, endpoint, uncover)
				if err != nil {
					return nil, err
				}
				moved[move{from, to}] += n
			}
		}
	}
	var out []Correction
	for m, n := range moved {
		if n == 0 {
			continue
		}
		c := Correction{Harness: "codex", From: m.from, To: m.to, Calls: n}
		switch {
		case m.from == openai && m.to != openai:
			c.Reason = "Codex turns on a custom model_provider were recorded as OpenAI's"
		case m.from == m.to:
			c.Reason = "Codex turns through a custom model_provider run on an API key, not the plan, and were recorded as covered"
		default:
			c.Reason = "OpenAI's models through a gateway on the operator's credential were moved to the gateway by an earlier correction"
		}
		record(ctx, store, m.to, map[string]any{"from": m.from, "to": m.to, "calls": n, "reason": c.Reason})
		out = append(out, c)
	}
	return out, nil
}

// CorrectOpencode renames opencode turns stored under a raw providerID
// that now maps to a TokenOps provider ("zai-coding-plan" is z.ai's,
// through its plan endpoint).
func CorrectOpencode(ctx context.Context, store *sqlite.Store) ([]Correction, error) {
	if store == nil {
		return nil, nil
	}
	stored, err := store.ProvidersFor(ctx, opencode.SourceTag)
	if err != nil {
		return nil, err
	}
	var out []Correction
	for _, from := range stored {
		to, endpoint, ok := opencode.Provider(from)
		if !ok || string(to) == from {
			continue
		}
		n, err := store.RenameProvider(ctx, opencode.SourceTag, from, string(to), endpoint)
		if err != nil {
			return out, err
		}
		if n == 0 {
			continue
		}
		c := Correction{Harness: "opencode", From: from, To: string(to), Endpoint: endpoint, Calls: n,
			Reason: "opencode turns were recorded under the client's provider ID, not the provider that bills them"}
		record(ctx, store, string(to), map[string]any{"from": from, "to": string(to), "endpoint": endpoint, "calls": n, "reason": c.Reason})
		out = append(out, c)
	}
	return out, nil
}
