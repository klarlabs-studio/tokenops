package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("Default() invalid: %v", err)
	}
}

func TestLoadAppliesYAMLAndDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	const yaml = `
listen: "0.0.0.0:9090"
log:
  level: debug
  format: json
shutdown:
  timeout: 5s
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "0.0.0.0:9090" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
	if cfg.Log.Level != "debug" || cfg.Log.Format != "json" {
		t.Errorf("Log = %+v", cfg.Log)
	}
	if cfg.Shutdown.Timeout != 5*time.Second {
		t.Errorf("Shutdown.Timeout = %s", cfg.Shutdown.Timeout)
	}
}

func TestLoadEnvOverridesWinOverFile(t *testing.T) {
	t.Setenv("TOKENOPS_LISTEN", "127.0.0.1:1234")
	t.Setenv("TOKENOPS_LOG_LEVEL", "warn")
	t.Setenv("TOKENOPS_SHUTDOWN_TIMEOUT", "30s")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "127.0.0.1:1234" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
	if cfg.Log.Level != "warn" {
		t.Errorf("Level = %q", cfg.Log.Level)
	}
	if cfg.Shutdown.Timeout != 30*time.Second {
		t.Errorf("Timeout = %s", cfg.Shutdown.Timeout)
	}
}

func TestLoadShutdownTimeoutIntegerSeconds(t *testing.T) {
	t.Setenv("TOKENOPS_SHUTDOWN_TIMEOUT", "7")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Shutdown.Timeout != 7*time.Second {
		t.Errorf("Timeout = %s", cfg.Shutdown.Timeout)
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	cases := map[string]Config{
		"empty listen": {Listen: "", Log: LogConfig{Level: "info", Format: "text"}, Shutdown: ShutdownConfig{Timeout: time.Second}},
		"bad level":    {Listen: ":1", Log: LogConfig{Level: "verbose", Format: "text"}, Shutdown: ShutdownConfig{Timeout: time.Second}},
		"bad format":   {Listen: ":1", Log: LogConfig{Level: "info", Format: "xml"}, Shutdown: ShutdownConfig{Timeout: time.Second}},
		"zero timeout": {Listen: ":1", Log: LogConfig{Level: "info", Format: "text"}, Shutdown: ShutdownConfig{Timeout: 0}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestLoadMissingFileErrors(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestEnvBoolOverrides(t *testing.T) {
	t.Run("TLS enabled", func(t *testing.T) {
		t.Setenv("TOKENOPS_TLS_ENABLED", "1")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.TLS.Enabled {
			t.Error("TLS.Enabled should be true")
		}
	})
	t.Run("TLS disabled", func(t *testing.T) {
		t.Setenv("TOKENOPS_TLS_ENABLED", "0")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.TLS.Enabled {
			t.Error("TLS.Enabled should be false")
		}
	})
	t.Run("storage enabled true", func(t *testing.T) {
		t.Setenv("TOKENOPS_STORAGE_ENABLED", "true")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.Storage.Enabled {
			t.Error("Storage.Enabled should be true")
		}
	})
	t.Run("storage enabled false", func(t *testing.T) {
		t.Setenv("TOKENOPS_STORAGE_ENABLED", "false")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Storage.Enabled {
			t.Error("Storage.Enabled should be false")
		}
	})
	t.Run("otel enabled on", func(t *testing.T) {
		t.Setenv("TOKENOPS_OTEL_ENABLED", "on")
		t.Setenv("TOKENOPS_OTEL_ENDPOINT", "http://localhost:4318")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.OTel.Enabled {
			t.Error("OTel.Enabled should be true")
		}
	})
	t.Run("otel enabled off", func(t *testing.T) {
		t.Setenv("TOKENOPS_OTEL_ENABLED", "off")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.OTel.Enabled {
			t.Error("OTel.Enabled should be false")
		}
	})
}

func TestRedactEnabledDefaultsTrue(t *testing.T) {
	var o OTelConfig
	if !o.RedactEnabled() {
		t.Error("RedactEnabled should default to true")
	}
}

func TestRedactEnabledExplicitFalse(t *testing.T) {
	v := false
	o := OTelConfig{Redact: &v}
	if o.RedactEnabled() {
		t.Error("RedactEnabled should be false when Redact = &false")
	}
}

func TestRedactEnabledExplicitTrue(t *testing.T) {
	v := true
	o := OTelConfig{Redact: &v}
	if !o.RedactEnabled() {
		t.Error("RedactEnabled should be true when Redact = &true")
	}
}
