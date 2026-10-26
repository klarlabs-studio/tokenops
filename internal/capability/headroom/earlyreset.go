package headroom

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// earlyResets are the early resets of each bound plan's windows that the
// readings show within the last day (plans.EarlyResetVisibleFor). The
// plan history guards them: a switch of plan is not a reset. When the
// history cannot be read, a switch cannot be ruled out, so none is reported.
func earlyResets(ctx context.Context, d Deps, now time.Time) []plans.EarlyReset {
	if d.Reader == nil || d.Config == nil {
		return nil
	}
	var history plans.History
	if d.PlanHistory != nil {
		h, err := d.PlanHistory()
		if err != nil {
			return nil
		}
		history = h
	}
	envs, err := d.Reader.ReadEvents(ctx, eventschema.EventTypePrompt, now.Add(-plans.EarlyResetLookback))
	if err != nil {
		return nil
	}
	var out []plans.EarlyReset
	for _, provider := range []eventschema.Provider{eventschema.ProviderAnthropic, eventschema.ProviderOpenAI} {
		if _, bound := d.Config.Plans[string(provider)]; !bound {
			continue
		}
		changed := func(from, to time.Time) bool { return history.ChangedBetween(string(provider), from, to) }
		out = append(out, plans.EarlyResetsIn(envs, provider, now.Add(-plans.EarlyResetVisibleFor), now, changed)...)
	}
	return out
}
