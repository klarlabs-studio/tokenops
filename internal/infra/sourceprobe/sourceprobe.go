// Package sourceprobe reads how fresh an ingestion source's ORIGIN is —
// the newest transcript on disk, the newest row in a client's own store.
//
// It lives here rather than in internal/cli because both the CLI status
// command and the ingestion daemon need the same answer, and the CLI
// imports the daemon, so the daemon cannot import back.
package sourceprobe

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/telemetry/opencodedb"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexjsonl"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/opencode"

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
		root := cfg.VendorUsage.CodexJSONL.Root
		out["codex-jsonl"] = func() (time.Time, bool) {
			if root == "" {
				r, err := codexjsonl.DefaultRoot()
				if err != nil {
					return time.Time{}, false
				}
				root = r
			}
			return newestJSONLAt(root)
		}
	}
	if cfg.VendorUsage.ClaudeCodeJSONL.Enabled {
		root := cfg.VendorUsage.ClaudeCodeJSONL.Root
		out["claude-code-jsonl"] = func() (time.Time, bool) {
			if root == "" {
				r, err := claudecodejsonl.DefaultRoot()
				if err != nil {
					return time.Time{}, false
				}
				root = r
			}
			return newestJSONLAt(root)
		}
	}
	if cfg.VendorUsage.OpenCode.Enabled {
		root := cfg.VendorUsage.OpenCode.Root
		out["opencode"] = func() (time.Time, bool) {
			if root == "" {
				r, err := opencode.DefaultRoot()
				if err != nil {
					return time.Time{}, false
				}
				root = r
			}
			return newestOpenCodeMessage(root)
		}
	}
	return out
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
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
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
	return opencodedb.Newest(dbPath)
}
