// Package sourceprobe reads how fresh an ingestion source's ORIGIN is —
// the newest transcript on disk, the newest row in a client's own store.
//
// It lives here rather than in internal/cli because both the CLI status
// command and the ingestion daemon need the same answer, and the CLI
// imports the daemon, so the daemon cannot import back.
package sourceprobe

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexjsonl"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexrollout"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/geminicli"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/opencode"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/pisessions"
	opencodestore "go.klarlabs.de/tokenops/internal/infra/opencodedb"

	_ "modernc.org/sqlite" // pure-Go driver registered as "sqlite"
)

// Only the local-transcript readers get one. A remote poller — the
// claude.ai cookie, the Admin API, the Copilot quota endpoint — has no
// cheap local origin to compare against, and one that is enabled and has
// ingested nothing genuinely is broken, because the vendor endpoint always
// has an answer. Their time-based warning is correct and is left alone.
// All builds the origin probes for the readers whose source of truth is
// on this machine.
func All(cfg config.Config) map[string]config.SourceProbe {
	out := map[string]config.SourceProbe{}
	if cfg.VendorUsage.CodexJSONL.Enabled {
		out["codex-jsonl"] = probeRoot(cfg.VendorUsage.CodexJSONL.Root, codexjsonl.DefaultRoot, newestJSONLAt)
	}
	if cfg.VendorUsage.ClaudeCodeJSONL.Enabled {
		out["claude-code-jsonl"] = probeRoot(cfg.VendorUsage.ClaudeCodeJSONL.Root, claudecodejsonl.DefaultRoot, newestJSONLAt)
	}
	if cfg.VendorUsage.OpenCode.Enabled {
		out["opencode"] = probeRoot(cfg.VendorUsage.OpenCode.Root, opencode.DefaultRoot, newestOpenCodeMessage)
	}
	if cfg.VendorUsage.GeminiCLI.Enabled {
		out["gemini-cli"] = probeRoot(cfg.VendorUsage.GeminiCLI.Root, geminicli.DefaultRoot, func(root string) (time.Time, bool) {
			return newestFoundAt([]string{root}, geminicli.FindSessionFiles)
		})
	}
	if cfg.VendorUsage.Pi.Enabled {
		root := cfg.VendorUsage.Pi.Root
		out["pi-sessions"] = func() (time.Time, bool) {
			if root != "" {
				return newestFoundAt([]string{root}, pisessions.FindSessionFiles)
			}
			roots, err := pisessions.DefaultRoots()
			if err != nil {
				return time.Time{}, false
			}
			if len(roots) == 0 {
				// DefaultRoots lists only the roots that exist.
				return time.Time{}, true
			}
			return newestFoundAt(roots, pisessions.FindSessionFiles)
		}
	}
	return out
}

// probeRoot probes the configured root, or the client's default one.
//
// A missing root is unknown when it was configured: a typo and an
// uninstalled client look alike from here, and only one of them means
// there is nothing to ingest. A missing default cannot be a typo: the
// client keeps nothing on this machine, so there is nothing to miss. It
// was unknown too, and an opencode used once weeks ago and since removed
// kept every spend figure marked untrusted as a critical, silent source.
func probeRoot(configured string, defaultRoot func() (string, error), newest func(string) (time.Time, bool)) config.SourceProbe {
	return func() (time.Time, bool) {
		if configured != "" {
			return newest(configured)
		}
		root, err := defaultRoot()
		if err != nil || !filepath.IsAbs(root) {
			// No home to resolve it under: nothing to tell.
			return time.Time{}, false
		}
		if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
			return time.Time{}, true
		}
		return newest(root)
	}
}

// newestFoundAt reports the modification time of the newest file a
// reader's own finder lists under roots, so the probe counts exactly what
// the reader would ingest: a Gemini CLI home holding only logs.json has
// nothing to read, however recent the file. No existing root is unknown,
// as in newestJSONLAt.
func newestFoundAt(roots []string, find func(string) ([]string, error)) (time.Time, bool) {
	var newest time.Time
	known := false
	for _, root := range roots {
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			continue
		}
		files, err := find(root)
		if err != nil {
			continue
		}
		known = true
		for _, f := range files {
			if fi, err := os.Stat(f); err == nil && fi.ModTime().After(newest) {
				newest = fi.ModTime()
			}
		}
	}
	if !known {
		return time.Time{}, false
	}
	return newest.UTC(), true
}

// newestJSONLAt reports the modification time of the newest transcript
// under root.
//
// A missing root is "unknown" rather than "empty": a client uninstalled, a
// path typo and a genuinely fresh install are indistinguishable from here,
// and only one of them means there is nothing to ingest. An existing root
// holding no transcripts is a real empty, which is why that case returns a
// zero time with ok=true.
func newestJSONLAt(root string) (time.Time, bool) {
	if root == "" {
		return time.Time{}, false
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return time.Time{}, false
	}
	var newest time.Time
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip it rather than fail the probe
		}
		// A cold Codex rollout is only ever its compressed copy.
		if d.IsDir() || (!strings.HasSuffix(path, ".jsonl") && !codexrollout.IsCompressed(path)) {
			return nil
		}
		fi, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
		return nil
	})
	if walkErr != nil {
		return time.Time{}, false
	}
	return newest.UTC(), true
}

// newestOpenCodeMessage reads the newest message timestamp from opencode's
// own SQLite store, in 1.x's and 2.x's tables alike, opened read-only so
// the probe can never disturb the client that owns it. Reading only 1.x's
// table made an upgraded opencode look idle from the day it upgraded.
func newestOpenCodeMessage(dbPath string) (time.Time, bool) {
	if dbPath == "" {
		return time.Time{}, false
	}
	return opencodestore.Newest(dbPath)
}
