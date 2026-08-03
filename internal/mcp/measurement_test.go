package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
)

// A spend figure of zero has two very different causes: nothing was spent, or
// nothing was measured. They were formatted identically, so a 27-day ingestion
// outage answered "$0.00, 700 tokens" to a question about a day that cut six
// releases — indistinguishable from a quiet afternoon.
func TestMeasurementQualityFlagsStaleIngestion(t *testing.T) {
	d := Deps{StaleSources: func() []config.StaleSource {
		return []config.StaleSource{{
			Name: "claude_code_jsonl", SourceTag: "claude-code-jsonl",
			WindowHours: 48, SilentFor: 27 * 24 * time.Hour,
		}}
	}}

	q := measurementQuality(d)
	if q == nil {
		t.Fatal("stale ingestion produced no measurement caveat")
	}
	if q["trusted"] != false {
		t.Errorf("trusted = %v, want false", q["trusted"])
	}
	if sev, _ := q["severity"].(string); sev != "critical" {
		t.Errorf("severity = %q, want critical", sev)
	}
	blob, _ := json.Marshal(q)
	if !strings.Contains(string(blob), "27 days") {
		t.Errorf("caveat does not state the gap: %s", blob)
	}
	// The caller has to understand what the number now means.
	if note, _ := q["note"].(string); !strings.Contains(note, "lower bound") {
		t.Errorf("note does not say the figure is a lower bound: %q", note)
	}
}

// When ingestion is healthy the payload must stay exactly as it was — no new
// key, no noise on the normal path.
func TestMeasurementQualitySilentWhenHealthy(t *testing.T) {
	if q := measurementQuality(Deps{StaleSources: func() []config.StaleSource { return nil }}); q != nil {
		t.Errorf("healthy ingestion produced a caveat: %v", q)
	}
	if q := measurementQuality(Deps{}); q != nil {
		t.Errorf("absent hook produced a caveat: %v", q)
	}
}

// The most severe source wins, so one critical gap is not hidden behind a
// milder one.
func TestMeasurementQualityReportsTheWorstSource(t *testing.T) {
	d := Deps{StaleSources: func() []config.StaleSource {
		return []config.StaleSource{
			{SourceTag: "opencode", WindowHours: 48, SilentFor: 50 * time.Hour},
			{SourceTag: "claude-code-jsonl", WindowHours: 48, SilentFor: 27 * 24 * time.Hour},
		}
	}}

	q := measurementQuality(d)
	if sev, _ := q["severity"].(string); sev != "critical" {
		t.Errorf("severity = %q, want the worst source's critical", sev)
	}
	if n, _ := q["stale_sources"].(int); n != 2 {
		t.Errorf("stale_sources = %v, want 2", q["stale_sources"])
	}
}
