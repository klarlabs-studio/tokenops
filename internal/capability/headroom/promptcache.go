package headroom

import (
	"context"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// PromptCacheShownCold is how long after its cache went cold a session
// still counts as the current one: past it, the countdown is left out.
const PromptCacheShownCold = time.Hour

// promptCacheLookback reaches the write that set the lifetime of a cache
// that could still be shown.
const promptCacheLookback = waste.CacheTTLExtended + PromptCacheShownCold + time.Hour

// PromptCache is the countdown to the current Claude Code session's prompt
// cache going cold: the session whose turn was recorded last, from when
// its last request went out (each turn's time less its latency) plus the
// lifetime its latest write bought. nil without storage, without a Claude
// Code session, or when its cache went cold over PromptCacheShownCold ago.
// It reads token counts, times and models only; the session's ID stays
// out of the answer.
func PromptCache(ctx context.Context, r Reader, now time.Time) *waste.CacheCountdown {
	if r == nil {
		return nil
	}
	envs, err := r.ReadEvents(ctx, eventschema.EventTypePrompt, now.Add(-promptCacheLookback))
	if err != nil {
		return nil
	}
	bySession := map[string][]*eventschema.Envelope{}
	var latest *eventschema.Envelope
	var current string
	for _, e := range envs {
		p, ok := e.Payload.(*eventschema.PromptEvent)
		if !ok || e.Source != claudecodejsonl.SourceTag || p.SessionID == "" {
			continue
		}
		bySession[p.SessionID] = append(bySession[p.SessionID], e)
		if latest == nil || e.Timestamp.After(latest.Timestamp) {
			latest, current = e, p.SessionID
		}
	}
	if latest == nil {
		return nil
	}
	session := bySession[current]
	sort.SliceStable(session, func(i, j int) bool { return session[i].Timestamp.Before(session[j].Timestamp) })
	turns := make([]waste.CacheTurn, 0, len(session))
	for _, e := range session {
		p := e.Payload.(*eventschema.PromptEvent)
		turns = append(turns, waste.CacheTurn{At: e.Timestamp, Latency: p.Latency, Model: p.RequestModel,
			CacheRead: p.CachedInputTokens, CacheWrite: p.CacheWriteInputTokens, CacheWrite1h: p.CacheWrite1hInputTokens})
	}
	last, ttl, ok := waste.ColdClock(turns)
	if !ok {
		return nil
	}
	c := waste.Countdown(last, ttl, now)
	if now.Sub(c.ExpiresAt) > PromptCacheShownCold {
		return nil
	}
	c.Client = "claude-code"
	return &c
}
