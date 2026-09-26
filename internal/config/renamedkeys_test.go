package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A renamed YAML key is silently ignored by the decoder, so a config still
// using the old spelling loads cleanly and the feature never runs — the
// exact shape of failure this project keeps removing. The rename is a clean
// break, which makes the error message the only migration aid there is.
func TestRetiredKeyIsRefusedWithItsReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "vendor_usage:\n  anthropic_cookie:\n    enabled: true\n    session_key: sk-ant-sid-x\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("a config using the retired key should be refused, not silently ignored")
	}
	for _, want := range []string{"anthropic_cookie", "claude_usage_meter", "vendor-usage setup claude-subscription"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name %q so the operator can act on it:\n%v", want, err)
		}
	}
	// Losing history is a real consequence of a clean break; saying so is
	// cheaper than someone discovering it from a count that dropped.
	if !strings.Contains(err.Error(), "not counted") {
		t.Errorf("the error should say old rows are not counted under the new name:\n%v", err)
	}
}

func TestCurrentKeyLoadsCleanly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "vendor_usage:\n  claude_usage_meter:\n    enabled: true\n    session_key: sk-ant-sid-x\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.VendorUsage.ClaudeUsageMeter.Enabled {
		t.Error("claude_usage_meter did not bind")
	}
}

// The source tag is what lands in the event store and in retention rules,
// so it has to move with the name rather than staying at the old spelling.
func TestSourceTagMatchesTheNewName(t *testing.T) {
	var found bool
	for _, s := range (Config{}).VendorUsageSources() {
		if s.SourceTag == "claude-usage-meter" {
			found = true
		}
		if s.SourceTag == "anthropic-cookie" {
			t.Error("the retired source tag is still registered")
		}
	}
	if !found {
		t.Error("claude-usage-meter is not in the source registry")
	}
}
