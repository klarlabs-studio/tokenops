package proxy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
	)
	if err := srv.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		shutdownCtx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	})
	waitListening(t, srv.Addr())
	return srv
}

func waitListening(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server never started listening on %s", addr)
}

func TestHealthzReturnsOK(t *testing.T) {
	srv := newTestServer(t)

	resp, err := http.Get("http://" + srv.Addr() + "/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v", body["status"])
	}
}

func TestReadyzReflectsMarkReady(t *testing.T) {
	srv := newTestServer(t)
	t.Cleanup(func() { MarkReady(false) })

	MarkReady(false)
	resp, err := http.Get("http://" + srv.Addr() + "/readyz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("not ready status = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	MarkReady(true)
	resp, err = http.Get("http://" + srv.Addr() + "/readyz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("ready status = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestVersionEndpoint(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get("http://" + srv.Addr() + "/version")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "version") {
		t.Errorf("missing version field: %s", body)
	}
}

func TestShutdownCancelsServing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
	)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitListening(t, srv.Addr())

	cancel()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + srv.Addr() + "/healthz")
		if err != nil {
			return
		}
		_ = resp.Body.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server still listening after shutdown")
}

func TestStartTwiceErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	})
	if err := srv.Start(ctx); err == nil {
		t.Fatal("expected error on second Start")
	}
}
