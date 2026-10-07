package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/observ"
	"go.klarlabs.de/tokenops/internal/events"
)

// newTestStartup returns a startup whose log lines land in the returned
// buffer.
func newTestStartup(cfg config.Config) (*startup, *bytes.Buffer) {
	var buf bytes.Buffer
	return &startup{cfg: cfg, logger: slog.New(slog.NewTextHandler(&buf, nil))}, &buf
}

func validConfig() config.Config {
	return config.Config{
		Listen:   "127.0.0.1:0",
		Log:      config.LogConfig{Level: "info", Format: "text"},
		Shutdown: config.ShutdownConfig{Timeout: time.Second},
	}
}

// A config that fails validation stops the boot before anything is
// constructed, with the error the daemon has always returned.
func TestRunWithLoggerRejectsInvalidConfig(t *testing.T) {
	err := RunWithLogger(context.Background(), config.Config{}, slog.New(slog.DiscardHandler))
	if err == nil || !strings.HasPrefix(err.Error(), "config: ") {
		t.Fatalf("err = %v, want a config: error", err)
	}
}

func TestAnnounceLogsTheBanner(t *testing.T) {
	s, buf := newTestStartup(validConfig())
	if err := s.announce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "tokenops daemon starting") {
		t.Errorf("banner missing:\n%s", buf)
	}
}

func TestResolveLegacyEventLog(t *testing.T) {
	s, _ := newTestStartup(validConfig())
	if err := s.resolveLegacyEventLog(context.Background()); err != nil || s.domainLogPath != "" {
		t.Fatalf("storage off: path %q err %v, want none", s.domainLogPath, err)
	}

	cfg := validConfig()
	dir := t.TempDir()
	cfg.Storage = config.StorageConfig{Enabled: true, Path: filepath.Join(dir, "db", "events.db")}
	s, buf := newTestStartup(cfg)
	if err := s.resolveLegacyEventLog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "db", "domain-events.jsonl"); s.domainLogPath != want {
		t.Errorf("path = %q, want %q", s.domainLogPath, want)
	}
	if !strings.Contains(buf.String(), "legacy domain event import path ready") {
		t.Errorf("log line missing:\n%s", buf)
	}
}

func TestBuildTransportOptionsAddsResilienceWhenEnabled(t *testing.T) {
	cfg := validConfig()
	s, _ := newTestStartup(cfg)
	s.eventCounter = observ.NewEventCounter()
	if err := s.buildTransportOptions(context.Background()); err != nil {
		t.Fatal(err)
	}
	base := len(s.opts)

	cfg.Resilience = config.ResilienceConfig{Enabled: true, FailureThreshold: 3}
	s, buf := newTestStartup(cfg)
	s.eventCounter = observ.NewEventCounter()
	if err := s.buildTransportOptions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.opts) != base+1 {
		t.Errorf("options = %d, want %d", len(s.opts), base+1)
	}
	if !strings.Contains(buf.String(), "resilience enabled") {
		t.Errorf("log line missing:\n%s", buf)
	}
}

func TestConfigureTLSMintsTheBundle(t *testing.T) {
	cfg := validConfig()
	s, _ := newTestStartup(cfg)
	if err := s.configureTLS(context.Background()); err != nil || len(s.opts) != 0 {
		t.Fatalf("tls off: %d options, err %v", len(s.opts), err)
	}

	cfg.TLS = config.TLSConfig{Enabled: true, CertDir: t.TempDir()}
	s, buf := newTestStartup(cfg)
	if err := s.configureTLS(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.opts) != 1 || !strings.Contains(buf.String(), "tls bundle ready") {
		t.Errorf("options = %d, log:\n%s", len(s.opts), buf)
	}
}

func TestConfigureAPIAuthUsesTheConfiguredToken(t *testing.T) {
	cfg := validConfig()
	cfg.Dashboard.AdminToken = "configured-token"
	s, _ := newTestStartup(cfg)
	if err := s.configureAPIAuth(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.dashToken != "configured-token" || len(s.opts) != 1 {
		t.Errorf("token %q, options %d", s.dashToken, len(s.opts))
	}
}

func TestConfigureRulesObservesTheBusUntilCleanup(t *testing.T) {
	cfg := validConfig()
	cfg.Rules = config.RulesConfig{Enabled: true, Root: t.TempDir(), RepoID: "repo"}
	s, buf := newTestStartup(cfg)
	s.events = &eventRuntime{Bus: events.NewAsync(events.NoopSink{}, events.Options{})}
	if err := s.configureRules(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.opts) != 1 || len(s.cleanups) != 1 {
		t.Errorf("options %d, cleanups %d; want 1 and 1", len(s.opts), len(s.cleanups))
	}
	if !strings.Contains(buf.String(), "rule intelligence enabled") {
		t.Errorf("log line missing:\n%s", buf)
	}
	s.runCleanups()
	_ = s.events.Bus.Close(time.Second)
}

func TestDeclarePlanCoverage(t *testing.T) {
	s, _ := newTestStartup(validConfig())
	if err := s.declarePlanCoverage(context.Background()); err != nil || len(s.opts) != 0 {
		t.Fatalf("no plans: %d options, err %v", len(s.opts), err)
	}
	cfg := validConfig()
	cfg.Plans = map[string]string{"anthropic": "claude-max-20x"}
	s, buf := newTestStartup(cfg)
	if err := s.declarePlanCoverage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.opts) != 1 || !strings.Contains(buf.String(), "plan-covered providers declared") {
		t.Errorf("options %d, log:\n%s", len(s.opts), buf)
	}
}

func TestConfigureRoutingSkipsWithoutARouterConfig(t *testing.T) {
	s, buf := newTestStartup(validConfig())
	if err := s.configureRouting(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.opts) != 0 || buf.Len() != 0 {
		t.Errorf("routing wired without a router config: %d options, log:\n%s", len(s.opts), buf)
	}
}

// Cleanups unwind newest first, as the deferred calls they replace did:
// the announcement goes before the rules observer, which goes before the
// event bus detaches.
func TestRunCleanupsUnwindsNewestFirst(t *testing.T) {
	s, _ := newTestStartup(validConfig())
	var got []int
	for i := range 3 {
		s.deferCleanup(func() { got = append(got, i) })
	}
	s.runCleanups()
	if len(got) != 3 || got[0] != 2 || got[1] != 1 || got[2] != 0 {
		t.Errorf("order = %v, want [2 1 0]", got)
	}
	s.runCleanups()
	if len(got) != 3 {
		t.Errorf("cleanups ran twice: %v", got)
	}
}
