package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

func applyEnvOverrides(cfg *Config) {
	applyCredentialEnv(cfg)
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
	if v := os.Getenv("TOKENOPS_TLS_ENABLED"); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			cfg.TLS.Enabled = true
		case "0", "false", "no", "off":
			cfg.TLS.Enabled = false
		}
	}
	if v := os.Getenv("TOKENOPS_TLS_CERT_DIR"); v != "" {
		cfg.TLS.CertDir = v
	}
	if v := os.Getenv("TOKENOPS_STORAGE_ENABLED"); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			cfg.Storage.Enabled = true
		case "0", "false", "no", "off":
			cfg.Storage.Enabled = false
		}
	}
	if v := os.Getenv("TOKENOPS_STORAGE_PATH"); v != "" {
		cfg.Storage.Path = v
	}
	if v := os.Getenv("TOKENOPS_OTEL_ENABLED"); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			cfg.OTel.Enabled = true
		case "0", "false", "no", "off":
			cfg.OTel.Enabled = false
		}
	}
	if v := os.Getenv("TOKENOPS_OTEL_ENDPOINT"); v != "" {
		cfg.OTel.Endpoint = v
	}
	if v := os.Getenv("TOKENOPS_OTEL_SERVICE_NAME"); v != "" {
		cfg.OTel.ServiceName = v
	}
	if v := os.Getenv("TOKENOPS_PRICING_PATH"); v != "" {
		cfg.Pricing.Path = v
	}
	for _, key := range []string{"openai", "anthropic", "gemini"} {
		envKey := "TOKENOPS_PROVIDER_" + strings.ToUpper(key) + "_URL"
		if v := os.Getenv(envKey); v != "" {
			if cfg.Providers == nil {
				cfg.Providers = make(map[string]string, 3)
			}
			cfg.Providers[key] = v
		}
		planEnvKey := "TOKENOPS_PLAN_" + strings.ToUpper(key)
		if v := os.Getenv(planEnvKey); v != "" {
			if cfg.Plans == nil {
				cfg.Plans = make(map[string]string, 3)
			}
			cfg.Plans[key] = v
		}
	}
}
