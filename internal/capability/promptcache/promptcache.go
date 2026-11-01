// Package promptcache times a Claude Code session's prompt cache: when it
// goes cold, from the event store for the glance and the coach's API, or
// from the end of the session's transcript for the status line. Both read
// timestamps, token counts and models only, never prompt text.
package promptcache

import (
	"context"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Reader reads the event store.
type Reader interface {
	ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error)
}

// ShownCold is how long after its cache went cold a session
// still counts as the current one: past it, the countdown is left out.
const ShownCold = time.Hour

// promptCacheLookback reaches the write that set the lifetime of a cache
// that could still be shown.
const promptCacheLookback = waste.CacheTTLExtended + ShownCold + time.Hour

// Current is the countdown to the current Claude Code session's prompt
// cache going cold: the session whose turn was recorded last, from when
// its last request went out (each turn's time less its latency) plus the
// lifetime its latest write bought. nil without storage, without a Claude
// Code session, or when its cache went cold over ShownCold ago.
// It reads token counts, times and models only; the session's ID stays
// out of the answer.
func Current(ctx context.Context, r Reader, now time.Time) *waste.CacheCountdown {
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
	if now.Sub(c.ExpiresAt) > ShownCold {
		return nil
	}
	c.Client = "claude-code"
	c.CachedTokens = waste.CachedPrefix(turns)
	return &c
}

// Expiry is when a session's prompt cache goes cold and how much it holds.
type Expiry struct {
	At time.Time
	// Tokens is the prompt cached (waste.CachedPrefix): what the next
	// prompt writes again once the cache is cold.
	Tokens int64
}

// TranscriptExpiry is when the session whose transcript is at path has its
// prompt cache go cold: the lifetime its latest write bought
// (waste.ColdClock) after its last request went out, which is when the
// last turn's request was sent or, when an entry such as a tool result
// follows the last reply, when that entry was. Only the last tailBytes
// are read. False when the transcript is not there or shows no cache use.
func TranscriptExpiry(path string) (Expiry, bool) {
	if path == "" {
		return Expiry{}, false
	}
	turns, lastEntry, err := claudecodejsonl.ReadTail(path, tailBytes)
	if err != nil || len(turns) == 0 {
		return Expiry{}, false
	}
	ct := make([]waste.CacheTurn, 0, len(turns))
	for _, t := range turns {
		ct = append(ct, waste.CacheTurn{At: t.Timestamp, Latency: t.Latency, Model: t.Model,
			CacheRead: t.CacheReadInputTokens, CacheWrite: t.CacheCreationInputTokens, CacheWrite1h: t.CacheCreation1hInputTokens})
	}
	last, ttl, ok := waste.ColdClock(ct)
	if !ok {
		return Expiry{}, false
	}
	if lastEntry.After(turns[len(turns)-1].Timestamp) && lastEntry.After(last) {
		last = lastEntry
	}
	return Expiry{At: last.Add(ttl), Tokens: waste.CachedPrefix(ct)}, true
}

// tailBytes is how much of the end of a transcript TranscriptExpiry
// reads: enough for the last few turns. The status line runs it every
// few seconds, and a long session's transcript runs to many megabytes.
const tailBytes = 256 << 10
