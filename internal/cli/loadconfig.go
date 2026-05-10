package cli

import (
	"fmt"
	"strconv"

	"github.com/felixgeelhaar/tokenops/internal/config"
)

// loadConfig resolves the effective configuration: file (if specified) +
// environment variables + flag overrides applied last. Validation runs
// after every override so flag values cannot smuggle in invalid state.
func loadConfig(rf *rootFlags) (config.Config, error) {
	cfg, err := config.Load(rf.configPath)
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
