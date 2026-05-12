package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/config"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/plans"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// PlanDeps wires the plan-headroom MCP tool. Config supplies the
// configured plans map; Store backs consumption queries.
type PlanDeps struct {
	Config *config.Config
	Store  *sqlite.Store
}

// planStoreReader adapts *sqlite.Store to plans.EventReader without
// dragging the sqlite dependency into the domain package.
type planStoreReader struct{ store *sqlite.Store }

func (r planStoreReader) ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	return r.store.Query(ctx, sqlite.Filter{Type: t, Since: since, Limit: 100_000})
}

// RegisterPlanTools mounts tokenops_plan_headroom on s. Returns an
// error when deps are incomplete so callers can surface the
// misconfiguration via the structured-error contract instead of a
// silent zero-data response.
func RegisterPlanTools(s *Server, d PlanDeps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_plan_headroom").
		Description("Return month-to-date consumption + overage risk for every configured subscription plan (Claude Max, ChatGPT Plus, Copilot, Cursor, etc.). Returns a structured `{error, hint}` payload when plans or storage are not configured.").
		Handler(func(ctx context.Context, _ emptyInput) (string, error) {
			return planHeadroom(ctx, d)
		})
	return nil
}

func planHeadroom(ctx context.Context, d PlanDeps) (string, error) {
	if d.Config == nil || len(d.Config.Plans) == 0 {
		return jsonString(map[string]string{
			"error": "plans_unconfigured",
			"hint":  "set `plans:` in config or TOKENOPS_PLAN_<PROVIDER>=<plan-name>",
		}), nil
	}
	if d.Store == nil {
		return jsonString(map[string]string{
			"error": "storage_disabled",
			"hint":  "run `tokenops init` then restart the daemon",
		}), nil
	}
	reader := planStoreReader{store: d.Store}
	now := time.Now().UTC()
	reports := make([]plans.HeadroomReport, 0, len(d.Config.Plans))
	for provider, planName := range d.Config.Plans {
		cons, err := plans.ConsumptionFor(ctx, reader, provider, now)
		if err != nil {
			return "", fmt.Errorf("consumption[%s]: %w", provider, err)
		}
		report, err := plans.ComputeHeadroom(planName, plans.HeadroomInputs{
			ConsumedTokens: cons.ConsumedTokens,
			Last7DayTokens: cons.Last7DayTokens,
			Now:            now,
		})
		if err != nil {
			return "", fmt.Errorf("headroom[%s]: %w", provider, err)
		}
		reports = append(reports, report)
	}
	out, err := json.MarshalIndent(map[string]any{"reports": reports}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
