package mcp

import (
	"context"
	"errors"
	"strings"

	"go.klarlabs.de/tokenops/internal/capability/actions"

	"go.klarlabs.de/tokenops/internal/capability/decisions"

	"go.klarlabs.de/tokenops/internal/config"
)

// ApprovalDeps wires the routing-approval tools. StorePath empty falls
// back to the conventional log location.
type ApprovalDeps struct {
	StorePath  string
	ConfigPath string
	// ApplyConfig makes a written config take effect; see applyConfig.
	ApplyConfig func() string
}

func (d ApprovalDeps) configPath() (string, error) {
	if d.ConfigPath != "" {
		return d.ConfigPath, nil
	}
	return config.DefaultPath()
}

type routingDecideInput struct {
	Key      string `json:"key" jsonschema:"description=Route identifier from tokenops_routing (action=proposals) (provider|from_model|to_model)"`
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
		Description("List model upgrades the proxy refused because they exceed your preferred model. Each entry offers a real choice: take the proposed model, or stay on your preferred one. Call this when the operator asks why a model was not switched, and surface any pending entry to them — nothing applies until they answer via tokenops_routing (action=decide).").
		Handler(func(_ context.Context, _ emptyInput) (string, error) {
			res, err := decisions.PendingProposals(d.StorePath)
			if err != nil {
				return "", inputError(err)
			}
			return jsonString(res), nil
		})

	s.Tool("tokenops_routing_decide").
		Description("Record the operator's answer to a pending routing proposal. approve = future matching requests route to the proposed model; deny = they stay on the model already requested. Only call this once the operator has actually chosen — it changes which model their requests run on.").
		Handler(func(_ context.Context, in routingDecideInput) (string, error) {
			res, err := actions.DecideRouting(d.StorePath, in.Key, in.Decision)
			if err != nil {
				return "", inputError(err)
			}
			return jsonString(res), nil
		})

	s.Tool("tokenops_preferred_model").
		Description("Get or set the preferred model for a provider. It acts as a ceiling: a routing rule that would move you to a pricier model is refused and referred to you rather than applied, while routes to cheaper models still apply automatically. Persists to config.yaml and restarts a supervised daemon so it takes effect.").
		Handler(func(_ context.Context, in preferredModelSetInput) (string, error) {
			path, err := d.configPath()
			if err != nil {
				return "", inputError(err)
			}
			if strings.TrimSpace(in.Provider) == "" {
				cfg, err := config.ReadMutable(path)
				if err != nil {
					return "", inputError(err)
				}
				return jsonString(map[string]any{"preferred_models": cfg.PreferredModels, "config": path}), nil
			}
			res, err := actions.SetPreferredModel(path, in.Provider, in.Model, in.Clear)
			if err != nil {
				return "", actionError(err)
			}
			return withNote(res, applyConfig(d.ApplyConfig)), nil
		})

	return nil
}
