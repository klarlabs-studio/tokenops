package cli

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// quotaLookupTimeout bounds the read so a busy store cannot hold a turn.
const quotaLookupTimeout = 2 * time.Second

// liveQuota opens the store read-only and asks the headroom capability for
// the live window of the plan bound to provider. Any failure returns nil,
// and the coach falls back to its dollar ladder rather than guess.
func liveQuota(ctx context.Context, cfg config.Config, provider eventschema.Provider, now time.Time) *coachhook.Quota {
	if !cfg.Storage.Enabled || cfg.Plans[string(provider)] == "" {
		return nil
	}
	path, err := resolveStorageReadPath("", cfg.Storage.Path)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, quotaLookupTimeout)
	defer cancel()
	store, err := sqlite.OpenReadOnly(ctx, path)
	if err != nil {
		return nil
	}
	defer func() { _ = store.Close() }()
	w, ok := headroom.LiveWindow(ctx, cfg, store, provider, now)
	if !ok {
		return nil
	}
	return &coachhook.Quota{Provider: string(provider), Window: w}
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
