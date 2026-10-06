package cli

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// quotaLookupTimeout bounds the read so a busy store cannot hold a turn.
const quotaLookupTimeout = 2 * time.Second

// planReading is what the coach knows about the plan a session draws on.
type planReading struct {
	// Quota is the live window, nil when none is fresh.
	Quota *coachhook.Quota
	// Flat is a flat-rate plan bound for the provider.
	Flat bool
	// Lost, on a flat plan with no live window, says why and what brings
	// it back; empty when there has never been a reading.
	Lost string
}

// readPlan opens the store read-only and asks the headroom capability for
// the live window of the plan bound to provider. Any failure leaves the
// window nil; on a flat-rate plan the coach then says the reading is lost
// rather than fall back to dollars nobody pays.
func readPlan(ctx context.Context, cfg config.Config, provider eventschema.Provider, now time.Time) planReading {
	name := cfg.Plans[string(provider)]
	p, known := plans.Lookup(name)
	out := planReading{Flat: known && name != plans.PayAsYouGo && !p.SpendDenominated}
	if !cfg.Storage.Enabled || name == "" {
		return out
	}
	path, err := resolveStorageReadPath("", cfg.Storage.Path)
	if err != nil {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, quotaLookupTimeout)
	defer cancel()
	store, err := sqlite.OpenReadOnly(ctx, path)
	if err != nil {
		return out
	}
	defer func() { _ = store.Close() }()
	all := headroom.LiveWindows(ctx, cfg, store, provider, now)
	if w, ok := plans.MostConstrained(all); ok {
		out.Quota = &coachhook.Quota{Provider: string(provider), Window: w, All: all}
		return out
	}
	if at, ok := headroom.LastReadingAt(ctx, store, provider); ok && out.Flat {
		out.Lost = readingLost(provider, now.Sub(at))
	}
	return out
}

// readingLost says how old a plan's newest reading is and what brings it
// back, per provider.
func readingLost(provider eventschema.Provider, age time.Duration) string {
	old := humanAge(age)
	switch provider {
	case eventschema.ProviderAnthropic:
		return "Claude's plan reading is " + old + " old, so how much of the window is left is unknown. " +
			"The claude.ai session has likely expired: sign in to claude.ai in your browser, or run " +
			"`tokenops vendor-usage setup claude-subscription`."
	case eventschema.ProviderOpenAI:
		return "Codex's plan reading is " + old + " old, so how much of the window is left is unknown. " +
			"Check the daemon is reading: `tokenops status`."
	}
	return ""
}

// humanAge words an age as "20h" or "3d".
func humanAge(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d >= time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return strconv.Itoa(max(1, int(d/time.Minute))) + "m"
}

// hookProvider is the provider whose plan a Stop event's session draws on.
// Codex writes its rollouts under ~/.codex; Claude Code everywhere else.
// Cursor and opencode have no live window meter, so they get none.
func hookProvider(in stopHookInput, cursor, opencode bool) (eventschema.Provider, bool) {
	switch {
	case cursor || opencode:
		return "", false
	case strings.Contains(filepath.ToSlash(in.TranscriptPath), "/.codex/"):
		return eventschema.ProviderOpenAI, true
	default:
		return eventschema.ProviderAnthropic, true
	}
}
