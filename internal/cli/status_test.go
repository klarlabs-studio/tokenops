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
	warn := "ingestion stale [critical]: claude-code-jsonl has produced no events for 27 days"
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
	withWarn, err := json.Marshal(statusResult{Warnings: []string{"ingestion stale [warning]: opencode has produced no events for 3 days"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(withWarn), `"warnings"`) {
		t.Errorf("expected warnings key in JSON: %s", withWarn)
	}
	if !strings.Contains(string(withWarn), "opencode has produced no events") {
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

// The daemon reports its drop count on /healthz; status has to turn that into
// a warning, because the number's only previous reader was a log line emitted
// at shutdown.
func TestStatusDropWarningFromHealthBody(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want bool
	}{
		{"dropped rows warn", map[string]any{"dropped_events": float64(30254)}, true},
		{"zero is silent", map[string]any{"dropped_events": float64(0)}, false},
		{"absent key is silent", map[string]any{"status": "ok"}, false},
		{"nil body is silent", nil, false},
		// An older daemon answers /healthz without the field. That is an
		// unknown, not a zero, and inventing "0 dropped" would report health
		// the daemon never claimed.
		{"non-numeric is silent", map[string]any{"dropped_events": "lots"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dropWarningFrom(endpointResult{Body: tc.body})
			if (got != "") != tc.want {
				t.Fatalf("dropWarningFrom(%v) = %q, want warning=%v", tc.body, got, tc.want)
			}
			if tc.want && !strings.Contains(got, "30254") {
				t.Fatalf("warning omits the count: %q", got)
			}
		})
	}
}

// On a supervised machine `tokenops start` starts a second daemon, so the
// offline status must name the supervisor's restart there.
func TestOfflineRemedyFitsTheMachine(t *testing.T) {
	if r := offlineRemedy(true); !strings.Contains(r, "tokenops daemon restart") || strings.Contains(r, "tokenops start`") {
		t.Errorf("supervised remedy = %q", r)
	}
	if r := offlineRemedy(false); !strings.Contains(r, "tokenops daemon install") {
		t.Errorf("unsupervised remedy = %q", r)
	}
}
