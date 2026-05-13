package cli

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/config"
)

// configWatcher polls the on-disk config file for changes and swaps a
// shared pointer when modtime moves forward. Poll-based rather than
// fsnotify-based to avoid pulling a new module dependency for a single
// feature; the 2-second tick is well below the threshold an operator
// notices ("I ran `tokenops plan set` then asked the agent — it picked
// up the change before I finished typing").
type configWatcher struct {
	path   string
	logger *slog.Logger
	cfg    atomic.Pointer[config.Config]
}

// newConfigWatcher seeds the pointer with the initial Config and
// starts the polling loop. Returns a Get accessor every tool can call
// to read the latest config without locking.
func newConfigWatcher(ctx context.Context, path string, initial config.Config, logger *slog.Logger) *configWatcher {
	w := &configWatcher{path: path, logger: logger}
	w.cfg.Store(&initial)
	if path == "" {
		return w
	}
	go w.run(ctx)
	return w
}

// Get returns the latest Config snapshot. Cheap (atomic load), so
// every tool call can re-fetch instead of caching a stale pointer.
func (w *configWatcher) Get() *config.Config {
	return w.cfg.Load()
}

// run polls every 2 seconds for an mtime change and reloads on hit.
// Errors don't kill the watcher — they log and back off. ctx
// cancellation stops the loop.
func (w *configWatcher) run(ctx context.Context) {
	var lastMod time.Time
	if info, err := os.Stat(w.path); err == nil {
		lastMod = info.ModTime()
	}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			info, err := os.Stat(w.path)
			if err != nil {
				continue
			}
			if !info.ModTime().After(lastMod) {
				continue
			}
			lastMod = info.ModTime()
			cfg, err := config.Load(w.path)
			if err != nil {
				w.logger.Warn("config reload failed", "err", err, "path", w.path)
				continue
			}
			prev := w.cfg.Load()
			w.cfg.Store(&cfg)
			w.logger.Info("config reloaded",
				"path", w.path,
				"plans_before", planKeys(prev),
				"plans_after", planKeys(&cfg),
			)
		}
	}
}

func planKeys(c *config.Config) []string {
	if c == nil {
		return nil
	}
	keys := make([]string, 0, len(c.Plans))
	for k := range c.Plans {
		keys = append(keys, k)
	}
	return keys
}
