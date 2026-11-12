package config

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A local source whose origin cannot be read stays a warning however long
// it has been quiet. Its silence measures nothing: a client the operator
// removed or stopped using looks the same as a reader that died, and
// grading the gap put a source unused for 43 days at critical on every
// status. A remote poller, with no origin to read, and a source whose
// origin shows data it missed, still escalate.
func TestCheckStaleIngestionEscalatesOnlyOnEvidence(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	silent := 43 * 24 * time.Hour

	tests := []struct {
		name          string
		probe         SourceProbe
		wantSeverity  string
		originUnknown bool
	}{
		{
			name:          "origin cannot be read",
			probe:         func() (time.Time, bool) { return time.Time{}, false },
			wantSeverity:  "warning",
			originUnknown: true,
		},
		{
			name:         "origin holds data the window missed",
			probe:        func() (time.Time, bool) { return now.Add(-time.Hour), true },
			wantSeverity: "critical",
		},
		{
			name:         "no probe: a remote poller",
			probe:        nil,
			wantSeverity: "critical",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{}
			cfg.VendorUsage.OpenCode.Enabled = true
			seer := &fakeSeer{
				counts: map[string]int64{},
				last:   map[string]time.Time{"opencode": now.Add(-silent)},
			}
			var probes map[string]SourceProbe
			if tt.probe != nil {
				probes = map[string]SourceProbe{"opencode": tt.probe}
			}
			stale, err := cfg.CheckStaleIngestion(context.Background(), seer, probes, StaleIngestionWindow, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(stale) != 1 {
				t.Fatalf("got %d stale sources, want 1: %+v", len(stale), stale)
			}
			s := stale[0]
			if s.OriginUnknown != tt.originUnknown {
				t.Errorf("OriginUnknown = %v, want %v", s.OriginUnknown, tt.originUnknown)
			}
			if got := s.Severity(); got != tt.wantSeverity {
				t.Errorf("severity = %q, want %q", got, tt.wantSeverity)
			}
			if !strings.Contains(s.Warning(), "["+tt.wantSeverity+"]") {
				t.Errorf("warning does not carry its severity: %s", s.Warning())
			}
		})
	}
}

// The warning for an unreadable origin says why it is not escalating and
// how to silence it for a client the operator no longer uses, rather than
// only telling them to restart a daemon that is running.
func TestStaleSourceWarningForAnUnreadableOrigin(t *testing.T) {
	s := StaleSource{
		Name: "opencode", SourceTag: "opencode", WindowHours: 48,
		SilentFor: 43 * 24 * time.Hour, OriginUnknown: true,
	}
	got := s.Warning()
	for _, want := range []string{
		"[warning]",
		"43 days",
		"its local data could not be read",
		"enabled: false under vendor_usage",
		"tokenops daemon restart",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("warning lacks %q: %s", want, got)
		}
	}
}
