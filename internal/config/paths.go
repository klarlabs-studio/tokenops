package config

import (
	"os"
	"path/filepath"
	"strings"
)

// expandHomePaths resolves a leading ~ in every path the config holds.
//
// The configuration guide writes them as ~/.tokenops/..., the way an
// operator does, and nothing expanded the ~: the daemon opened a
// directory literally named "~" under its working directory (/ under
// launchd), and a ~ pricing override failed to load. Load expands; the
// file keeps what the operator wrote, since ReadMutable does not.
func expandHomePaths(cfg *Config) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	for _, p := range []*string{
		&cfg.Storage.Path,
		&cfg.Pricing.Path,
		&cfg.Rules.Root,
		&cfg.TLS.CertDir,
		&cfg.VendorUsage.ClaudeCode.Path,
		&cfg.VendorUsage.ClaudeCodeJSONL.Root,
		&cfg.VendorUsage.CodexJSONL.Root,
		&cfg.VendorUsage.OpenCode.Root,
		&cfg.VendorUsage.GeminiCLI.Root,
		&cfg.VendorUsage.Pi.Root,
		&cfg.VendorUsage.CodexAppServer.Path,
	} {
		*p = expandHome(*p, home)
	}
}

// expandHome replaces a leading ~ or ~/ in p with home.
func expandHome(p, home string) string {
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	}
	return p
}
