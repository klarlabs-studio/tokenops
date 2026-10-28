package codexjsonl

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

// tokenCountWithLimit is a token_count record whose rate_limits snapshot
// names its limit, as codex-rs/protocol RateLimitSnapshot serialises it
// (limit_id and limit_name are null for the default "codex" bucket).
func tokenCountWithLimit(limitFields, primaryPct string) string {
	return `{"timestamp":"2026-04-17T08:46:14.725Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":200,"cached_input_tokens":0,"output_tokens":30,"reasoning_output_tokens":0,"total_tokens":230},"model_context_window":258400},"rate_limits":{` +
		limitFields + `"primary":{"used_percent":` + primaryPct + `,"window_minutes":300,"resets_at":1776431887},"secondary":{"used_percent":2.0,"window_minutes":10080,"resets_at":1777018687},"credits":null,"individual_limit":null,"spend_control_reached":null,"plan_type":"plus","rate_limit_reached_type":null}}}`
}

func TestReadFileKeysRateLimitsByLimitID(t *testing.T) {
	line := tokenCountWithLimit(`"limit_id":"codex_secondary","limit_name":"Example Model",`, "7.0")
	var got []Turn
	st := &readState{sessionID: "s", modelProvider: "openai"}
	if _, err := readLines(strings.NewReader(line+"\n"), st, true, func(t Turn) error {
		got = append(got, t)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d turns, want 1", len(got))
	}
	rl := got[0].RateLimits
	if rl.LimitID != "codex_secondary" || rl.LimitName != "Example Model" {
		t.Errorf("limit = %q %q, want codex_secondary Example Model", rl.LimitID, rl.LimitName)
	}
	if rl.PrimaryUsedPercent != 7.0 {
		t.Errorf("primary used = %v, want 7", rl.PrimaryUsedPercent)
	}
}

// The default bucket has null limit_id, and so does every snapshot from
// before Codex named limits. Their envelopes must stay as they were.
func TestNewEnvelopeOmitsTheLimitForTheDefaultBucket(t *testing.T) {
	for _, fields := range []string{``, `"limit_id":null,"limit_name":null,`, `"limit_id":"codex","limit_name":null,`} {
		st := &readState{sessionID: "s", modelProvider: "openai"}
		var turn Turn
		_, _ = readLines(strings.NewReader(tokenCountWithLimit(fields, "10.0")+"\n"), st, true, func(t Turn) error {
			turn = t
			return nil
		})
		env := newEnvelope(turn, "", "")
		if _, ok := env.Attributes["limit_id"]; ok {
			t.Errorf("fields %q: default bucket envelope carries limit_id = %q", fields, env.Attributes["limit_id"])
		}
	}
}

func TestNewEnvelopeNamesANonDefaultLimit(t *testing.T) {
	st := &readState{sessionID: "s", modelProvider: "openai"}
	var turn Turn
	_, _ = readLines(strings.NewReader(tokenCountWithLimit(`"limit_id":"codex_secondary","limit_name":null,`, "10.0")+"\n"), st, true, func(t Turn) error {
		turn = t
		return nil
	})
	env := newEnvelope(turn, "", "")
	if got := env.Attributes["limit_id"]; got != "codex_secondary" {
		t.Errorf("limit_id attribute = %q, want codex_secondary", got)
	}
}

// A token_count record whose rate_limits no longer decodes is a shape
// Codex has changed. It must be counted and reported to health, while
// the turns around it are still read.
func TestPollerReportsAnUnrecognisedTokenCountShape(t *testing.T) {
	root := t.TempDir()
	body := codexFile + tokenCountWithLimit(`"limit_id":{"id":"codex"},`, "10.0") + "\n"
	if err := os.WriteFile(filepath.Join(root, "rollout-shape.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	bus := &captureBus{}
	reg := freshness.NewRegistry()
	p := NewPoller(bus, PollerOptions{
		Root: root, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health: reg.For(SourceTag),
	})
	p.scan(context.Background(), root)
	if got := bus.PublishedCount(); got != 1 {
		t.Errorf("published %d turns, want the 1 readable turn", got)
	}
	poll := reg.For(SourceTag).Poll()
	if poll.LastError == nil || !strings.Contains(poll.LastError.Error(), "unrecognised") {
		t.Errorf("health last error = %v, want an unrecognised-shape error", poll.LastError)
	}
}

func TestPollerReportsASuccessfulScanToHealth(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rollout-ok.jsonl"), []byte(codexFile), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := freshness.NewRegistry()
	p := NewPoller(&captureBus{}, PollerOptions{
		Root: root, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health: reg.For(SourceTag),
	})
	p.scan(context.Background(), root)
	poll := reg.For(SourceTag).Poll()
	if poll.LastError != nil || poll.LastSuccessAt.IsZero() {
		t.Errorf("health after a clean scan = %+v, want a success", poll)
	}
}

// CODEX_HOME moves the default scan root, as it moves Codex's own.
func TestDefaultRootHonoursCodexHome(t *testing.T) {
	want := filepath.Join(t.TempDir(), "codex-elsewhere", "sessions")
	t.Setenv("CODEX_HOME", filepath.Dir(want))
	got, err := DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("DefaultRoot = %q, want %q", got, want)
	}
}
