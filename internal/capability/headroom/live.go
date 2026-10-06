package headroom

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodeoauth"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudestatusline"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexappserver"
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

// liveSources names, per provider, where window readings are stored and a
// key every reading from that source carries. Claude's come from the
// claude.ai meter and from Claude Code's status line, in the same shape;
// a status line reading may hold only a gateway spend limit, so it is
// found by its granularity rather than by a window.
var liveSources = map[eventschema.Provider][]struct{ source, key string }{
	eventschema.ProviderAnthropic: {
		{claudeusagemeter.SourceTag, "five_hour_used_pct"},
		{claudestatusline.SourceTag, "granularity"},
		{claudecodeoauth.SourceTag, "five_hour_used_pct"},
	},
	eventschema.ProviderOpenAI: {
		{codexjsonl.SourceTag, "primary_used_pct"},
		{codexappserver.SourceTag, "primary_used_pct"},
	},
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
	src, ok := liveSources[provider]
	if !ok {
		return nil
	}
	newest := map[string]*eventschema.Envelope{}
	for _, s := range src {
		attrs, at, ok, err := r.LatestAttributesBySource(ctx, s.source, s.key, now.Add(-LiveFreshness))
		if err != nil || !ok {
			continue
		}
		newest[s.source] = &eventschema.Envelope{Source: s.source, Timestamp: at, Attributes: attrs}
	}
	if len(newest) == 0 {
		return nil
	}
	return plans.QuotaWindowsFromAttributes(provider, plans.MergeReadings(newest))
}

// LastReadingAt is when provider's newest window reading was recorded, at
// any age. ok is false when there has never been one: a meter never set
// up, as against one that has stopped.
func LastReadingAt(ctx context.Context, r AttributeReader, provider eventschema.Provider) (time.Time, bool) {
	if r == nil {
		return time.Time{}, false
	}
	var newest time.Time
	for _, s := range liveSources[provider] {
		if _, at, ok, err := r.LatestAttributesBySource(ctx, s.source, s.key, time.Time{}); err == nil && ok && at.After(newest) {
			newest = at
		}
	}
	return newest, !newest.IsZero()
}
