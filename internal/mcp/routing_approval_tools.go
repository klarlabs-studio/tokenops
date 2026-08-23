package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval"
)

// ApprovalDeps wires the routing-approval tools. StorePath empty falls
// back to the conventional log location.
type ApprovalDeps struct {
	StorePath  string
	ConfigPath string
}

func (d ApprovalDeps) store() (*routingapproval.Store, error) {
	path := d.StorePath
	if path == "" {
		p, err := routingapproval.DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	return routingapproval.Open(path)
}

func (d ApprovalDeps) configPath() (string, error) {
	if d.ConfigPath != "" {
		return d.ConfigPath, nil
	}
	return config.DefaultPath()
}

type routingDecideInput struct {
	Key      string `json:"key" jsonschema:"description=Route identifier from tokenops_routing_proposals (provider|from_model|to_model)"`
	Decision string `json:"decision" jsonschema:"enum=approve,enum=deny,description=approve routes future matching requests to the proposed model; deny keeps you on the model you already asked for"`
}

type preferredModelSetInput struct {
	Provider string `json:"provider" jsonschema:"description=Provider name, e.g. anthropic"`
	Model    string `json:"model,omitempty" jsonschema:"description=The model you want to stay on. Omit with clear=true to remove the ceiling."`
	Clear    bool   `json:"clear,omitempty" jsonschema:"description=Remove the preferred model for this provider"`
}

// RegisterApprovalTools exposes the preferred-model ceiling: the pending
// upgrades the proxy refused, the decision that resolves one, and the
// setting itself.
//
// This is the surface where the operator is actually asked. The proxy
// cannot block an in-flight request on a human, so it refuses the
// upgrade, forwards the model the client asked for, and leaves the
// choice here for the agent to raise in conversation.
func RegisterApprovalTools(s *Server, d ApprovalDeps) error {
	if s == nil {
		return errors.New("mcp: nil server")
	}

	s.Tool("tokenops_routing_proposals").
		Description("List model upgrades the proxy refused because they exceed your preferred model. Each entry offers a real choice: take the proposed model, or stay on your preferred one. Call this when the operator asks why a model was not switched, and surface any pending entry to them — nothing applies until they answer via tokenops_routing_decide.").
		Handler(func(_ context.Context, _ emptyInput) (string, error) {
			store, err := d.store()
			if err != nil {
				return "", err
			}
			pending, err := store.Pending()
			if err != nil {
				return "", err
			}
			if len(pending) == 0 {
				return jsonString(map[string]any{
					"pending": []any{},
					"note":    "no upgrades are waiting on you",
				}), nil
			}
			out := make([]map[string]any, 0, len(pending))
			for _, p := range pending {
				entry := map[string]any{
					"key":             p.Key,
					"provider":        p.Provider,
					"requested_model": p.From,
					"proposed_model":  p.To,
					"preferred_model": p.Preferred,
					"times_seen":      p.Seen,
					"reason":          p.Reason,
					"question": fmt.Sprintf(
						"Routing wants to switch %s → %s. Approve, or stay on your preferred %s?",
						p.From, p.To, p.Preferred),
				}
				if p.Priced {
					entry["extra_usd_per_million_io_tokens"] = p.DeltaUSD
				} else {
					entry["pricing"] = "unverifiable — no rate card for one of the models, so the route was refused rather than guessed at"
				}
				out = append(out, entry)
			}
			return jsonString(map[string]any{
				"pending": out,
				"note":    "ask the operator before deciding; resolve with tokenops_routing_decide",
			}), nil
		})

	s.Tool("tokenops_routing_decide").
		Description("Record the operator's answer to a pending routing proposal. approve = future matching requests route to the proposed model; deny = they stay on the model already requested. Only call this once the operator has actually chosen — it changes which model their requests run on.").
		Handler(func(_ context.Context, in routingDecideInput) (string, error) {
			store, err := d.store()
			if err != nil {
				return "", err
			}
			key := strings.TrimSpace(in.Key)
			if key == "" {
				return "", errors.New("key is required (from tokenops_routing_proposals)")
			}
			state, err := store.Load()
			if err != nil {
				return "", err
			}
			st, ok := state[key]
			if !ok {
				return "", errors.New("no routing proposal with key " + key)
			}

			var decision, chosen string
			switch strings.ToLower(strings.TrimSpace(in.Decision)) {
			case "approve", "approved":
				decision, chosen = "approved", st.To
			case "deny", "denied":
				decision, chosen = "denied", st.From
			default:
				return "", errors.New(`decision must be "approve" or "deny"`)
			}
			if err := store.Decide(key, decision, chosen); err != nil {
				return "", err
			}
			return jsonString(map[string]any{
				"key":      key,
				"decision": decision,
				"model":    chosen,
				"note":     "applies to matching requests from now on; no restart needed",
			}), nil
		})

	s.Tool("tokenops_preferred_model").
		Description("Get or set the preferred model for a provider. It acts as a ceiling: a routing rule that would move you to a pricier model is refused and referred to you rather than applied, while routes to cheaper models still apply automatically. Persists to config.yaml; the daemon applies it on restart.").
		Handler(func(_ context.Context, in preferredModelSetInput) (string, error) {
			path, err := d.configPath()
			if err != nil {
				return "", err
			}
			cfg, err := config.ReadMutable(path)
			if err != nil {
				return "", err
			}
			provider := strings.TrimSpace(in.Provider)
			switch {
			case provider == "":
				return jsonString(map[string]any{
					"preferred_models": cfg.PreferredModels,
					"config":           path,
				}), nil
			case in.Clear:
				delete(cfg.PreferredModels, provider)
			default:
				model := strings.TrimSpace(in.Model)
				if model == "" {
					return "", errors.New("model is required unless clear=true")
				}
				if cfg.PreferredModels == nil {
					cfg.PreferredModels = map[string]string{}
				}
				cfg.PreferredModels[provider] = model
			}
			if err := config.WriteMutable(path, cfg); err != nil {
				return "", err
			}
			return jsonString(map[string]any{
				"preferred_models": cfg.PreferredModels,
				"config":           path,
				"note":             restartHint,
			}), nil
		})

	return nil
}
