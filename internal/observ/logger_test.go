package observ

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNewLoggerJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, "info", "json")
	log.Info("hello", "k", "v")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("expected JSON output: %v\nraw: %s", err, buf.String())
	}
	if got["msg"] != "hello" || got["k"] != "v" {
		t.Errorf("unexpected log record: %+v", got)
	}
}

func TestNewLoggerTextFormatDefault(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, "info", "text")
	log.Info("hi")
	if !strings.Contains(buf.String(), "msg=hi") {
		t.Errorf("expected text record, got %q", buf.String())
	}
}

func TestNewLoggerLevelFiltersBelow(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, "warn", "text")
	log.Info("dropped")
	if buf.Len() != 0 {
		t.Errorf("expected no output for info<warn, got %q", buf.String())
	}
	log.Warn("kept")
	if !strings.Contains(buf.String(), "kept") {
		t.Errorf("expected warn record, got %q", buf.String())
	}
}

func TestNewLoggerUnknownLevelFallsBackToInfo(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, "verbose", "text")
	log.Debug("dropped")
	if buf.Len() != 0 {
		t.Errorf("expected info-level fallback to drop debug, got %q", buf.String())
	}
	log.Info("kept")
	if !strings.Contains(buf.String(), "kept") {
		t.Errorf("expected info record, got %q", buf.String())
	}
}
