package state

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

func TestSourceHealthOf(t *testing.T) {
	tests := []struct {
		name      string
		in        []SourceReport
		wantLen   int
		unhealthy int
	}{
		{"nil becomes empty", nil, 0, 0},
		{"healthy only", []SourceReport{{Tag: "a", State: freshness.StateHealthy}}, 1, 0},
		{"one failing", []SourceReport{
			{Tag: "a", State: freshness.StateHealthy},
			{Tag: "b", State: freshness.StateFailing, Severity: freshness.SeverityDegraded},
		}, 2, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SourceHealthOf(tt.in)
			if got.Sources == nil {
				t.Fatal("Sources is nil; want an empty list")
			}
			if len(got.Sources) != tt.wantLen || got.Unhealthy != tt.unhealthy {
				t.Errorf("got %d sources, %d unhealthy; want %d, %d", len(got.Sources), got.Unhealthy, tt.wantLen, tt.unhealthy)
			}
		})
	}
}

// Each source that needs attention is worded by what is wrong with it, so
// the remedy fits: a refused reader needs its sign-in, not a daemon
// restart, and a quiet one that is merely unused says nothing.
func TestSourceWarnings(t *testing.T) {
	reports := []SourceReport{
		{Tag: "claude-code-jsonl", State: freshness.StateHealthy, Severity: freshness.SeverityOK},
		{Tag: "gemini-cli", State: freshness.StateIdle, Severity: freshness.SeverityOK},
		{Tag: "codex-jsonl", State: freshness.StateStale, Severity: freshness.SeverityWarning, NeverSeen: true, Window: 48 * time.Hour},
		{Tag: "cursor-web", State: freshness.StateFailing, Severity: freshness.SeverityWarning, LastError: "cursor: 401 unauthorized"},
		{Tag: "github-copilot", State: freshness.StateFailing, Severity: freshness.SeverityDegraded, ReaderStopped: true, LastError: "panic: boom"},
	}
	got := SourceWarnings(reports)
	if len(got.Stale) != 1 || len(got.Failing) != 2 {
		t.Fatalf("stale %v, failing %v; want 1 and 2", got.Stale, got.Failing)
	}
	want := config.StaleSource{SourceTag: "codex-jsonl", WindowHours: 48}.Warning()
	if got.Stale[0] != want {
		t.Errorf("stale warning = %q, want the existing wording %q", got.Stale[0], want)
	}
	for _, sub := range []string{"cursor-web", "401 unauthorized", "tokenops vendor-usage status"} {
		if !strings.Contains(got.Failing[0], sub) {
			t.Errorf("refused warning %q lacks %q", got.Failing[0], sub)
		}
	}
	if strings.Contains(got.Failing[0], "daemon restart") {
		t.Errorf("refused warning %q sends the operator to restart the daemon", got.Failing[0])
	}
	for _, sub := range []string{"github-copilot", "stopped", "panic: boom", "tokenops daemon restart"} {
		if !strings.Contains(got.Failing[1], sub) {
			t.Errorf("stopped warning %q lacks %q", got.Failing[1], sub)
		}
	}
	if all := SourceWarnings(reports[:2]); len(all.Stale)+len(all.Failing) != 0 {
		t.Errorf("healthy and idle sources warned: %+v", all)
	}
}
