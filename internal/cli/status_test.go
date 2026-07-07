package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// writeStatusText renders a warnings section only when warnings are
// present; the daemon lines are unchanged either way.
func TestWriteStatusTextRendersWarnings(t *testing.T) {
	warn := "ingestion stale: claude-code-jsonl has 0 events in the last 48h — reconnect the poller"
	res := statusResult{
		Health:   endpointResult{Status: 200},
		Ready:    endpointResult{Status: 200},
		Version:  endpointResult{Status: 200},
		Warnings: []string{warn},
	}
	var buf bytes.Buffer
	if err := writeStatusText(&buf, "http://127.0.0.1:7878", res); err != nil {
		t.Fatalf("writeStatusText: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "warnings:") {
		t.Errorf("expected warnings header; got:\n%s", out)
	}
	if !strings.Contains(out, warn) {
		t.Errorf("expected warning line; got:\n%s", out)
	}
}

func TestWriteStatusTextOmitsWarningsWhenEmpty(t *testing.T) {
	res := statusResult{
		Health:  endpointResult{Status: 200},
		Ready:   endpointResult{Status: 200},
		Version: endpointResult{Status: 200},
	}
	var buf bytes.Buffer
	if err := writeStatusText(&buf, "http://127.0.0.1:7878", res); err != nil {
		t.Fatalf("writeStatusText: %v", err)
	}
	if strings.Contains(buf.String(), "warnings") {
		t.Errorf("did not expect a warnings section; got:\n%s", buf.String())
	}
}

// The --json shape carries warnings when present and omits the key when
// empty (omitempty), so machine callers can branch on presence.
func TestStatusJSONWarnings(t *testing.T) {
	withWarn, err := json.Marshal(statusResult{Warnings: []string{"ingestion stale: opencode has 0 events in the last 48h"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(withWarn), `"warnings"`) {
		t.Errorf("expected warnings key in JSON: %s", withWarn)
	}
	if !strings.Contains(string(withWarn), "opencode has 0 events") {
		t.Errorf("expected warning string in JSON: %s", withWarn)
	}

	noWarn, err := json.Marshal(statusResult{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(noWarn), "warnings") {
		t.Errorf("expected warnings key omitted when empty: %s", noWarn)
	}
}
