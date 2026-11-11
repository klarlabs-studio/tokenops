package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	if err := os.WriteFile(cfgPath, []byte("plans:\n  anthropic: claude-max\n"), 0o600); err != nil {
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

// A vendor reading whose limit was not read keeps its spend, and the
// terminal does not call the account one without a limit.
func TestPlanHeadroomSaysTheLimitWasNotRead(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "events.db")
	st, err := sqlite.Open(context.Background(), db, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	reading := &eventschema.Envelope{
		ID:            "fw-limit-unread",
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     time.Now().UTC().Add(-time.Minute),
		Source:        "fireworks-usage",
		Attributes: map[string]string{
			"granularity": "quota_snapshot", "billing": "per_token", "scope": "account", "account_id": "acme",
			"extra_usage_used": "15.00", "extra_usage_limit": "0.00", "extra_usage_currency": "USD",
			"extra_usage_limit_reached": "false", "extra_usage_limit_unknown": "true",
		},
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderFireworks, Status: 200},
	}
	if err := st.AppendBatch(context.Background(), []*eventschema.Envelope{reading}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("plans:\n  fireworks: "+plans.PayAsYouGo+"\n"), 0o600); err != nil {
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
	if len(reports) != 1 || reports[0].SpendUSD != 15 || !reports[0].SpendLimitUnknown || reports[0].SpendLimitUSD != 0 {
		t.Fatalf("reports = %+v", reports)
	}
	out, err = executeRoot(t, "--config", cfgPath, "plan", "headroom", "--db", db)
	if err != nil {
		t.Fatalf("plan headroom: %v\n%s", err, out)
	}
	for _, want := range []string{"15.00 USD (limit not read)", "the account's spend limit could not be read"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "no limit") || strings.Contains(out, "no spend limit") {
		t.Errorf("an unread limit reads as none:\n%s", out)
	}
}
