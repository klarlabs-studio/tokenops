package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// `plan headroom` never read the vendor's own window %: with the Claude
// usage meter set up, the terminal still showed an estimate while the MCP
// tool showed Anthropic's figure. Both now assemble the same inputs.
func TestPlanHeadroomReadsTheVendorWindow(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "events.db")
	st, err := sqlite.Open(context.Background(), db, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	reading := &eventschema.Envelope{
		ID:            "ack-window",
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     time.Now().UTC().Add(-time.Minute),
		Source:        "claude-usage-meter",
		Attributes:    map[string]string{"five_hour_used_pct": "42.00"},
		Payload:       &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, Status: 200},
	}
	if err := st.AppendBatch(context.Background(), []*eventschema.Envelope{reading}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("plans:\n  anthropic: claude-max-20x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := executeRoot(t, "--config", cfgPath, "plan", "headroom", "--db", db, "--json")
	if err != nil {
		t.Fatalf("plan headroom: %v\n%s", err, out)
	}
	var reports []plans.HeadroomReport
	if err := json.Unmarshal([]byte(out), &reports); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(reports) != 1 || reports[0].WindowPct != 42 {
		t.Errorf("window pct = %+v, want the meter's 42%%", reports)
	}
}
