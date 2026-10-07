package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func writeOverrides(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pricing.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The operator's negotiated rates win over the catalog in every period.
func TestSpendEngineLayersTheOverrideFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	path := writeOverrides(t, "currency: USD\nrates:\n  anthropic:\n    claude-opus-4-7:\n      input_per_million: 1\n      output_per_million: 2\n")
	eng, err := SpendEngine(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := eng.Table().Lookup(eventschema.ProviderAnthropic, "claude-opus-4-7")
	if err != nil {
		t.Fatal(err)
	}
	if r.InputPerMillion != 1 || r.OutputPerMillion != 2 {
		t.Errorf("rate %+v, want the negotiated 1/2", r)
	}
}

func TestSpendEngineRefusesAMalformedOverrideFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := SpendEngine(writeOverrides(t, "rates: [not, a, table"), nil); err == nil {
		t.Error("a malformed override file built an engine; it is operator misconfiguration to surface")
	}
}
