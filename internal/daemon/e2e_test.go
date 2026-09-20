package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/daemonhint"
)

// TestE2EDaemonBootHealthShutdown drives the daemon end-to-end:
// boot → /healthz → /readyz → /version → /api/domain-events → shutdown.
// Exercises the full composition root (sqlite, bus, audit subscriber,
// domain-events JSONL, control endpoints) without provider routes.
func TestE2EDaemonBootHealthShutdown(t *testing.T) {
	port, err := freePort()
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	dir := t.TempDir()
	cfg := config.Config{
		Listen: net.JoinHostPort("127.0.0.1", port),
		Log:    config.LogConfig{Level: "info", Format: "text"},
		Storage: config.StorageConfig{
			Enabled: true,
			Path:    filepath.Join(dir, "events.db"),
		},
		Shutdown: config.ShutdownConfig{Timeout: 2 * time.Second},
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, cfg, io.Discard)
	}()

	base := "http://" + cfg.Listen
	if !waitFor(base+"/readyz", 3*time.Second) {
		cancel()
		<-errCh
		t.Fatal("daemon did not become ready")
	}

	for _, path := range []string{"/healthz", "/readyz", "/version"} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if resp.StatusCode != 200 {
			t.Errorf("%s status = %d", path, resp.StatusCode)
		}
		// Drain before closing. Closing an unread body leaves the
		// connection active rather than idle, and Server.Shutdown waits
		// for active connections — which is how this test intermittently
		// spent its whole shutdown budget waiting on itself.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	// /api/* is credentialed. This test used to GET /api/domain-events bare
	// and assert 200, which is how the route's missing auth looked correct
	// for as long as it did.
	bare, err := http.Get(base + "/api/domain-events")
	if err != nil {
		t.Fatalf("GET /api/domain-events: %v", err)
	}
	_, _ = io.Copy(io.Discard, bare.Body)
	_ = bare.Body.Close()
	if bare.StatusCode != 401 {
		t.Errorf("/api/domain-events without a credential = %d, want 401", bare.StatusCode)
	}

	// /api/domain-events shape, with the token the daemon wrote to its URL
	// hint — the same path the CLI and the MCP server take.
	tok := daemonhint.Token()
	if tok == "" {
		t.Fatal("daemon wrote no dashboard token to its URL hint")
	}
	req, err := http.NewRequest(http.MethodGet, base+"/api/domain-events", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/domain-events: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("/api/domain-events with a credential = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var ev struct {
		Counts       map[string]int64 `json:"counts"`
		Total        int64            `json:"total"`
		AuditDropped int64            `json:"audit_dropped"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Errorf("decode events: %v", err)
	}
	if ev.Counts == nil {
		t.Errorf("events.counts nil")
	}
	// Counts are lifetime totals; the daemon must also say when they
	// happened, or a months-old alert reads as a current one.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err == nil {
		if _, ok := raw["spans"]; !ok {
			t.Errorf("/api/domain-events has no spans: %s", body)
		}
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("daemon exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not shut down within 5s")
	}
}

// TestE2EDaemonRejectsUnusableConfig asserts that boot fails with a
// clear error when cfg.Validate would reject the input.
func TestE2EDaemonRejectsUnusableConfig(t *testing.T) {
	port, _ := freePort()
	cfg := config.Config{
		Listen:   net.JoinHostPort("127.0.0.1", port),
		Shutdown: config.ShutdownConfig{Timeout: 0}, // invalid
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := Run(ctx, cfg, io.Discard); err == nil {
		t.Fatal("expected error with zero shutdown timeout")
	}
}

func freePort() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer func() { _ = l.Close() }()
	_, port, err := net.SplitHostPort(l.Addr().String())
	return port, err
}

func waitFor(url string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			// Same drain-before-close as above. This polls every 50ms
			// until the daemon is ready, so an undrained body here can
			// leak a whole handful of connections into the shutdown.
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
