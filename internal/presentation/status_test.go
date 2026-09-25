package presentation

import "testing"

func TestForStatus(t *testing.T) {
	cases := []struct {
		state string
		level string
	}{
		{"ready", "clear"},
		{"degraded", "attention"},
		{"not_configured", "action_required"},
		{"not_ready", "unavailable"},
		{"unknown", "unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			if got := ForStatus(tc.state).Level; got != tc.level {
				t.Errorf("ForStatus(%q).Level = %q, want %q", tc.state, got, tc.level)
			}
		})
	}
}
