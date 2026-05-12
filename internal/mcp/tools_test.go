package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// execTool runs the named tool with the supplied input value (which is
// marshalled to JSON and unmarshalled into the handler's input struct
// by mcp-go). Returns the handler's text result as a string.
func execTool(t *testing.T, srv *Server, name string, args any) string {
	t.Helper()
	tool, ok := srv.GetTool(name)
	if !ok {
		t.Fatalf("no tool %q registered", name)
	}
	var raw json.RawMessage
	switch v := args.(type) {
	case nil:
		raw = json.RawMessage(`{}`)
	case json.RawMessage:
		raw = v
	case []byte:
		raw = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal args for %s: %v", name, err)
		}
		raw = b
	}
	out, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	s, ok := out.(string)
	if !ok {
		t.Fatalf("%s: result is %T, expected string", name, out)
	}
	return s
}

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
	got := jsonString(make(chan int))
	if !strings.HasPrefix(got, "error:") {
		t.Errorf("jsonString(channel) = %q, want error: prefix", got)
	}
}

func TestSpendSummaryInputToFilter(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		var w spendSummaryInput
		f, err := w.toFilter()
		if err != nil {
			t.Fatal(err)
		}
		if !f.Since.IsZero() || !f.Until.IsZero() {
			t.Error("expected zero times for empty args")
		}
	})

	t.Run("since RFC3339", func(t *testing.T) {
		w := spendSummaryInput{Since: "2026-05-01T00:00:00Z"}
		f, err := w.toFilter()
		if err != nil {
			t.Fatal(err)
		}
		if !f.Since.Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("Since = %v", f.Since)
		}
	})

	t.Run("since duration", func(t *testing.T) {
		w := spendSummaryInput{Since: "3d"}
		f, err := w.toFilter()
		if err != nil {
			t.Fatal(err)
		}
		if f.Since.IsZero() {
			t.Fatal("Since is zero")
		}
	})

	t.Run("until RFC3339", func(t *testing.T) {
		w := spendSummaryInput{Until: "2026-05-10T00:00:00Z"}
		f, err := w.toFilter()
		if err != nil {
			t.Fatal(err)
		}
		if !f.Until.Equal(time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("Until = %v", f.Until)
		}
	})

	t.Run("invalid since", func(t *testing.T) {
		w := spendSummaryInput{Since: "bad"}
		if _, err := w.toFilter(); err == nil {
			t.Fatal("expected error for invalid since")
		}
	})

	t.Run("invalid until", func(t *testing.T) {
		w := spendSummaryInput{Until: "bad"}
		if _, err := w.toFilter(); err == nil {
			t.Fatal("expected error for invalid until")
		}
	})
}
