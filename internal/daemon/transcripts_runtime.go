package daemon

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/version"
)

const (
	// transcriptSaveEvery is how old the saved transcript cache may get
	// while the daemon runs. A stop saves it, so this is for the start
	// after a crash, a kill or a power cut, which then reads a day of
	// transcripts and not two weeks. The save writes the whole cache, so
	// it is not made more often.
	transcriptSaveEvery = 24 * time.Hour
	// transcriptSaveCheck is how often the daemon looks whether a save is
	// due. After a start that restored nothing, the first look is the
	// first save: the first scans are through by then.
	transcriptSaveCheck = 30 * time.Minute
)

// transcriptsPath is where the shared transcript cache is saved across
// restarts.
func transcriptsPath() string { return filepath.Join(findings.DefaultDir(), "transcripts.gob") }

// transcriptsKey is the key this build saves the transcript cache under
// and reads it back by.
func transcriptsKey() string { return transcriptsKeyFor(version.Version, version.Commit) }

// transcriptsKeyFor names the code that fills the transcript cache, not
// the build: most upgrades leave that code alone, and one that read the
// transcripts again for its new version number alone ran hot for minutes
// for nothing. A build that names no commit, or was made from a tree with
// changes no commit holds, has no key and neither saves nor loads: its
// reading code may differ from what the revision was made from, and its
// save would be read back by the installed daemon.
func transcriptsKeyFor(ver, commit string) string {
	if commit == "" || commit == "unknown" || strings.HasSuffix(ver, "-dirty") {
		return ""
	}
	return sessions.TranscriptsRevision
}

// loadTranscripts fills the cache from the last save and reports whether
// it did: without it, the first scans after a start, which every upgrade
// and login does, read every transcript of two weeks again, minutes of
// several cores.
func loadTranscripts(c *sessions.TranscriptCache, path, key string, logger *slog.Logger) bool {
	if key == "" {
		return false
	}
	start := time.Now()
	err := c.Load(path, key)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false
	case err != nil:
		logger.Info("transcript cache not restored; the first scans read the transcripts", "reason", err.Error())
		return false
	}
	logger.Info("transcript cache restored", "files", c.Len(), "took", time.Since(start).Round(time.Millisecond))
	return true
}

// saveTranscripts saves the cache for the next start and reports whether
// it did.
func saveTranscripts(c *sessions.TranscriptCache, path, key string, logger *slog.Logger) bool {
	if c == nil || key == "" {
		return false
	}
	start := time.Now()
	if err := c.Save(path, key); err != nil {
		logger.Warn("transcript cache not saved; the next start reads the transcripts", "err", err)
		return false
	}
	logger.Info("transcript cache saved", "files", c.Len(), "took", time.Since(start).Round(time.Millisecond))
	return true
}

// startTranscriptSaveRuntime keeps the saved transcript cache no older
// than transcriptSaveEvery while the daemon runs. saved is when the file
// last held what the cache held: the restore at start, or never.
func startTranscriptSaveRuntime(cache *sessions.TranscriptCache, saved time.Time, sup *lifecycle.Supervisor, logger *slog.Logger) {
	key := transcriptsKey()
	if cache == nil || key == "" {
		return
	}
	sup.Go("transcript-cache-save", func(ctx context.Context) error {
		runTranscriptSaves(ctx, cache, transcriptsPath(), key, saved, transcriptSaveCheck, transcriptSaveEvery, logger)
		return nil
	})
}

// runTranscriptSaves looks every check whether the save is older than
// every, and saves when it is. An empty cache is not saved: no scan has
// run yet, and the file may hold what the next start can use. A save that
// failed is tried again at the next look.
func runTranscriptSaves(ctx context.Context, c *sessions.TranscriptCache, path, key string, saved time.Time, check, every time.Duration, logger *slog.Logger) {
	tick := time.NewTicker(check)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if c.Len() == 0 || (!saved.IsZero() && time.Since(saved) < every) {
			continue
		}
		if saveTranscripts(c, path, key, logger) {
			saved = time.Now()
		}
	}
}
