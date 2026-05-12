package proxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestResilienceProxyHappyPath verifies an SSE upstream stream flows
// through the resilience-wrapped proxy with per-chunk flushes intact.
func TestResilienceProxyHappyPath(t *testing.T) {
	const events = 3
	consumed := make(chan struct{}, events)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("upstream lost http.Flusher")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i := 0; i < events; i++ {
			_, _ = fmt.Fprintf(w, "data: event-%d\n\n", i)
			f.Flush()
			select {
			case <-consumed:
			case <-time.After(2 * time.Second):
				t.Errorf("upstream timed out waiting for event %d to be consumed", i)
				return
			}
		}
	}))
	defer upstream.Close()

	routes, err := BuildProviderRoutes(map[string]string{"openai": upstream.URL})
	if err != nil {
		t.Fatalf("BuildProviderRoutes: %v", err)
	}
	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithProviderRoutes(routes),
		WithResilience(ResilienceConfig{
			FirstByteTimeout: 500 * time.Millisecond,
			IdleTimeout:      500 * time.Millisecond,
			TotalTimeout:     5 * time.Second,
		}),
	)
	ctx, cancel := context.WithCancel(context.Background())
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

	resp, err := http.Get("http://" + srv.Addr() + "/openai/v1/messages")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	buf := make([]byte, 64)
	got := 0
	for got < events {
		n, err := resp.Body.Read(buf)
		if err != nil && err != io.EOF {
			t.Fatalf("read after %d events: %v", got, err)
		}
		if n > 0 {
			got++
			consumed <- struct{}{}
		}
	}
}

// TestResilienceProxyInvalidConfig surfaces the streamtimeout
// validation error through proxy.Server.Start.
func TestResilienceProxyInvalidConfig(t *testing.T) {
	routes, err := BuildProviderRoutes(nil)
	if err != nil {
		t.Fatalf("BuildProviderRoutes: %v", err)
	}
	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithProviderRoutes(routes),
		WithResilience(ResilienceConfig{}), // all zero → invalid
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err == nil {
		t.Errorf("expected error from invalid resilience config, got nil")
		shutdownCtx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	}
}
