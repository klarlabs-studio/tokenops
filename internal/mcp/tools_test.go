package mcp

import (
	"strings"
	"testing"
	"time"
)

func TestParseTimeOrDurationRFC3339(t *testing.T) {
	ref := "2026-05-10T12:00:00Z"
	got, err := parseTimeOrDuration(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("parseTimeOrDuration(%q) = %v", ref, got)
	}
}

func TestParseTimeOrDurationDays(t *testing.T) {
	got, err := parseTimeOrDuration("7d")
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(got) < 6*24*time.Hour || time.Since(got) > 8*24*time.Hour {
		t.Errorf("parseTimeOrDuration(7d) = %v, expected ~7 days ago", got)
	}
}

func TestParseTimeOrDurationGoDuration(t *testing.T) {
	got, err := parseTimeOrDuration("24h")
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(got) < 23*time.Hour || time.Since(got) > 25*time.Hour {
		t.Errorf("parseTimeOrDuration(24h) = %v, expected ~24h ago", got)
	}
}

func TestParseTimeOrDurationInvalid(t *testing.T) {
	_, err := parseTimeOrDuration("not-a-time")
	if err == nil {
		t.Fatal("expected error for invalid input")
	}
}

func TestJSONStringValid(t *testing.T) {
	got := jsonString(map[string]int{"a": 1})
	if !strings.Contains(got, `"a": 1`) {
		t.Errorf("jsonString = %q, want a:1", got)
	}
}

func TestJSONStringError(t *testing.T) {
	// Marshalling a channel returns an error.
	got := jsonString(make(chan int))
	if !strings.HasPrefix(got, "error:") {
		t.Errorf("jsonString(channel) = %q, want error: prefix", got)
	}
}

func TestWindowArgsToFilter(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		var w windowArgs
		f, err := w.toFilter()
		if err != nil {
			t.Fatal(err)
		}
		if !f.Since.IsZero() || !f.Until.IsZero() {
			t.Error("expected zero times for empty args")
		}
	})

	t.Run("since RFC3339", func(t *testing.T) {
		w := windowArgs{Since: "2026-05-01T00:00:00Z"}
		f, err := w.toFilter()
		if err != nil {
			t.Fatal(err)
		}
		if f.Since.IsZero() {
			t.Fatal("Since is zero")
		}
		if !f.Since.Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("Since = %v", f.Since)
		}
	})

	t.Run("since duration", func(t *testing.T) {
		w := windowArgs{Since: "3d"}
		f, err := w.toFilter()
		if err != nil {
			t.Fatal(err)
		}
		if f.Since.IsZero() {
			t.Fatal("Since is zero")
		}
	})

	t.Run("until RFC3339", func(t *testing.T) {
		w := windowArgs{Until: "2026-05-10T00:00:00Z"}
		f, err := w.toFilter()
		if err != nil {
			t.Fatal(err)
		}
		if f.Until.IsZero() {
			t.Fatal("Until is zero")
		}
		if !f.Until.Equal(time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("Until = %v", f.Until)
		}
	})

	t.Run("invalid since", func(t *testing.T) {
		w := windowArgs{Since: "bad"}
		_, err := w.toFilter()
		if err == nil {
			t.Fatal("expected error for invalid since")
		}
	})

	t.Run("invalid until", func(t *testing.T) {
		w := windowArgs{Until: "bad"}
		_, err := w.toFilter()
		if err == nil {
			t.Fatal("expected error for invalid until")
		}
	})
}
