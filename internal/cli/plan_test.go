package cli

import "testing"

func TestFriendlyDurations(t *testing.T) {
	for in, want := range map[string]string{
		"164h9m0s": "6d 20h", "1h7m0s": "1h 7m", "5h0m0s": "5h", "12m0s": "12m", "168h0m0s": "7d", "nonsense": "nonsense",
	} {
		if got := friendlyDuration(in); got != want {
			t.Errorf("friendlyDuration(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"168h0m0s": "week", "24h0m0s": "day", "5h0m0s": "5h"} {
		if got := windowName(in); got != want {
			t.Errorf("windowName(%q) = %q, want %q", in, got, want)
		}
	}
}
