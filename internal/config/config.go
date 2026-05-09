// Package config loads TokenOps daemon configuration from a YAML file with
// environment-variable overrides. The schema is intentionally small at this
// stage; subsequent tasks (proxy-providers, optimizer, observability) extend
// it with their own sections.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root daemon configuration.
type Config struct {
	Listen    string            `yaml:"listen"`
	Log       LogConfig         `yaml:"log"`
	Shutdown  ShutdownConfig    `yaml:"shutdown"`
	Providers map[string]string `yaml:"providers"`
}

// LogConfig configures the structured logger.
type LogConfig struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // json | text
}

// ShutdownConfig configures graceful shutdown behaviour.
type ShutdownConfig struct {
	Timeout time.Duration `yaml:"timeout"`
}

// Default returns the built-in defaults. The daemon is local-first by default
// and binds to loopback so a fresh install never accidentally exposes the
// proxy on the network.
func Default() Config {
	return Config{
		Listen: "127.0.0.1:7878",
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		Shutdown: ShutdownConfig{
			Timeout: 15 * time.Second,
		},
	}
}

// Load resolves configuration in order of precedence: defaults, optional YAML
// file (path may be empty), and environment variables. Environment variables
// always win.
func Load(path string) (Config, error) {
	cfg := Default()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config %q: %w", path, err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %q: %w", path, err)
		}
	}

	applyEnvOverrides(&cfg)

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks the configuration for unrecoverable errors.
func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen address must not be empty")
	}
	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid log level %q", c.Log.Level)
	}
	switch strings.ToLower(c.Log.Format) {
	case "json", "text":
	default:
		return fmt.Errorf("invalid log format %q", c.Log.Format)
	}
	if c.Shutdown.Timeout <= 0 {
		return fmt.Errorf("shutdown.timeout must be positive, got %s", c.Shutdown.Timeout)
	}
	return nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("TOKENOPS_LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := os.Getenv("TOKENOPS_LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("TOKENOPS_LOG_FORMAT"); v != "" {
		cfg.Log.Format = v
	}
	if v := os.Getenv("TOKENOPS_SHUTDOWN_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Shutdown.Timeout = d
		} else if secs, err := strconv.Atoi(v); err == nil {
			cfg.Shutdown.Timeout = time.Duration(secs) * time.Second
		}
	}
	for _, key := range []string{"openai", "anthropic", "gemini"} {
		envKey := "TOKENOPS_PROVIDER_" + strings.ToUpper(key) + "_URL"
		if v := os.Getenv(envKey); v != "" {
			if cfg.Providers == nil {
				cfg.Providers = make(map[string]string, 3)
			}
			cfg.Providers[key] = v
		}
	}
}
