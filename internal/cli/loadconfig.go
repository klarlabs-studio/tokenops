package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"

	"github.com/felixgeelhaar/tokenops/internal/config"
)

// loadConfig resolves the effective configuration: file (if specified) +
// environment variables + flag overrides applied last. Validation runs
// after every override so flag values cannot smuggle in invalid state.
//
// When --config is not passed, falls back to the file `tokenops init`
// writes ($XDG_CONFIG_HOME/tokenops/config.yaml or
// ~/.config/tokenops/config.yaml). This means `tokenops plan list`,
// `tokenops scorecard`, and friends pick up the same plans the user
// just bound via `tokenops plan set` without an explicit flag.
func loadConfig(rf *rootFlags) (config.Config, error) {
	configPath := rf.configPath
	if configPath == "" {
		if defaultPath, err := defaultConfigPath(); err == nil {
			if _, statErr := os.Stat(defaultPath); statErr == nil {
				configPath = defaultPath
			} else if !errors.Is(statErr, fs.ErrNotExist) {
				return config.Config{}, fmt.Errorf("stat %s: %w", defaultPath, statErr)
			}
		}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return config.Config{}, err
	}
	if rf.listen != "" {
		cfg.Listen = rf.listen
	}
	if rf.logLevel != "" {
		cfg.Log.Level = rf.logLevel
	}
	if rf.logFormat != "" {
		cfg.Log.Format = rf.logFormat
	}
	if rf.tlsFlag != "" {
		v, err := strconv.ParseBool(rf.tlsFlag)
		if err != nil {
			return config.Config{}, fmt.Errorf("invalid --tls value %q: %w", rf.tlsFlag, err)
		}
		cfg.TLS.Enabled = v
	}
	if rf.certDir != "" {
		cfg.TLS.CertDir = rf.certDir
	}
	if err := cfg.Validate(); err != nil {
		return config.Config{}, fmt.Errorf("invalid effective config: %w", err)
	}
	return cfg, nil
}
