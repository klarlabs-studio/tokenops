package headroom

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexjsonl"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// LiveFreshness is how old a meter reading may be and still describe the
// window now. The claude.ai meter polls every few minutes and Codex records
// its limits on every turn, so an older reading means the source stopped.
const LiveFreshness = 30 * time.Minute

// AttributeReader returns the newest event attributes a source recorded
// that carry key, no older than since.
type AttributeReader interface {
	LatestAttributesBySource(ctx context.Context, source, key string, since time.Time) (map[string]string, time.Time, bool, error)
}

// liveSource names where each provider's window reading is stored and a
// key every reading carries.
var liveSource = map[eventschema.Provider]struct{ source, key string }{
	eventschema.ProviderAnthropic: {claudeusagemeter.SourceTag, "five_hour_used_pct"},
	eventschema.ProviderOpenAI:    {codexjsonl.SourceTag, "primary_used_pct"},
}

// LiveWindow returns the most constrained window of the flat-rate plan
// bound to provider, from its newest meter reading. ok is false when no
// known plan is bound, the provider has no window meter, or no reading is
// fresher than LiveFreshness: callers then fall back rather than guess.
func LiveWindow(ctx context.Context, cfg config.Config, r AttributeReader, provider eventschema.Provider, now time.Time) (plans.QuotaWindow, bool) {
	return plans.MostConstrained(LiveWindows(ctx, cfg, r, provider, now))
}

// LiveWindows returns every window of the newest fresh reading for the plan
// bound to provider, or none under the same conditions as LiveWindow.
func LiveWindows(ctx context.Context, cfg config.Config, r AttributeReader, provider eventschema.Provider, now time.Time) []plans.QuotaWindow {
	if r == nil {
		return nil
	}
	if _, ok := plans.Lookup(cfg.Plans[string(provider)]); !ok {
		return nil
	}
	src, ok := liveSource[provider]
	if !ok {
		return nil
	}
	attrs, _, ok, err := r.LatestAttributesBySource(ctx, src.source, src.key, now.Add(-LiveFreshness))
	if err != nil || !ok {
		return nil
	}
	return plans.QuotaWindowsFromAttributes(provider, attrs)
}
