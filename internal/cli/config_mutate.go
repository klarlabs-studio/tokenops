package cli

import (
	"go.klarlabs.de/tokenops/internal/config"
)

// Thin wrappers over the config package's shared mutation helpers so
// existing CLI call sites keep their names. The implementations moved
// to internal/config when the MCP config tools gained write access —
// CLI and MCP must target the same on-disk file with identical
// read/validate/write semantics.

func defaultConfigPath() (string, error) { return config.DefaultPath() }

func readMutableConfig(path string) (config.Config, error) { return config.ReadMutable(path) }

func writeMutableConfig(path string, cfg config.Config) error {
	return config.WriteMutable(path, cfg)
}
