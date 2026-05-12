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

	"github.com/felixgeelhaar/tokenops/internal/config"
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

	for _, path := range []string{"/healthz", "/readyz", "/version", "/api/domain-events"} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if resp.StatusCode != 200 {
			t.Errorf("%s status = %d", path, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	// /api/domain-events shape.
	resp, _ := http.Get(base + "/api/domain-events")
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
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
