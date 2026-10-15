package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Every config an older init wrote carries `enabled: false` for every
// local reader it did not turn on, so that value says nothing about what
// the operator chose. Only a reader switched off through `vendor-usage
// enable --disable` (which records switched_off) stays off; the others are
// turned on when their data is on this machine.
func TestTurnOnLocalReadersTreatsAnOldEnabledFalseAsNeverSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	old := []byte(`vendor_usage:
  claude_code_jsonl:
    enabled: true
  codex_jsonl:
    enabled: false
  opencode:
    enabled: false
  gemini_cli:
    enabled: false
    switched_off: true
  pi:
    enabled: false
`)
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}

	got := cfg.TurnOnLocalReaders([]string{"claude-code-jsonl", "opencode", "gemini-cli", "pi-sessions"})

	if want := []string{"opencode", "pi-sessions"}; !reflect.DeepEqual(got, want) {
		t.Errorf("turned on %v, want %v", got, want)
	}
	if !cfg.VendorUsage.OpenCode.Enabled || !cfg.VendorUsage.Pi.Enabled {
		t.Error("a reader whose data is present and that was never switched off stayed off")
	}
	if cfg.VendorUsage.GeminiCLI.Enabled {
		t.Error("a reader the operator switched off was turned back on")
	}
	if cfg.VendorUsage.CodexJSONL.Enabled {
		t.Error("a reader with no data on this machine was turned on")
	}
	if again := cfg.TurnOnLocalReaders([]string{"opencode"}); len(again) != 0 {
		t.Errorf("a second pass turned on %v; it should say so once", again)
	}
}

func TestSetLocalReaderRecordsTheOperatorsChoice(t *testing.T) {
	var cfg Config
	if !cfg.SetLocalReader("opencode", false) {
		t.Fatal("opencode is a local reader")
	}
	if cfg.VendorUsage.OpenCode.Enabled || !cfg.VendorUsage.OpenCode.SwitchedOff {
		t.Errorf("--disable: %+v", cfg.VendorUsage.OpenCode)
	}
	cfg.SetLocalReader("opencode", true)
	if !cfg.VendorUsage.OpenCode.Enabled || cfg.VendorUsage.OpenCode.SwitchedOff {
		t.Errorf("enable: %+v", cfg.VendorUsage.OpenCode)
	}
	if cfg.SetLocalReader("cursor", true) {
		t.Error("cursor calls a vendor with a login; it is not a local reader")
	}
}
