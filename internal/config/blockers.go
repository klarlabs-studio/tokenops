package config

import (
	"os"
)

// Blockers returns the stable, machine-readable list of subsystem gates
// that prevent the daemon from returning populated data. Order is fixed
// so callers can diff successive snapshots. Returns a non-nil empty
// slice when nothing is gated so JSON serialises as [] not null.
func (c Config) Blockers() []string {
	blockers := []string{}
	if !c.Storage.Enabled {
		blockers = append(blockers, "storage_disabled")
	}
	if !c.Rules.Enabled {
		blockers = append(blockers, "rules_disabled")
	}
	if len(c.Providers) == 0 {
		blockers = append(blockers, "providers_unconfigured")
	}
	// A rules.root that no longer exists fails silently: the analyser
	// walks an empty tree and reports success, so a moved or renamed
	// repository degrades rule intelligence to nothing with no signal.
	// An empty root is fine — it means "use the working directory".
	if c.Rules.Enabled && c.Rules.Root != "" && !dirExists(c.Rules.Root) {
		blockers = append(blockers, "rules_root_missing")
	}
	return blockers
}

// dirExists reports whether path is a directory we can stat.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// NextActionsFor maps blockers to operator-facing remediation steps and
// deduplicates so a single `tokenops init` call surfaces once even when
// it resolves multiple blockers. Returns a non-nil empty slice when
// blockers is empty.
func NextActionsFor(blockers []string) []string {
	out := []string{}
	if len(blockers) == 0 {
		return out
	}
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, b := range blockers {
		switch b {
		case "storage_disabled", "rules_disabled":
			add("run `tokenops init` then restart the daemon")
		case "providers_unconfigured":
			add("run `tokenops provider set <name> <url>` (e.g. `tokenops provider set anthropic https://api.anthropic.com`)")
		case "rules_root_missing":
			add("set rules.root in config.yaml to a directory that exists (the configured path is gone — did the repo move?)")
		}
	}
	return out
}
