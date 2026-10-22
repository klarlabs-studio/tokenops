package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func startTimeoutProxy(t *testing.T, upstream string, bodyTimeout time.Duration) *Server {
	t.Helper()
	routes, err := BuildProviderRoutes(map[string]string{"openai": upstream})
	if err != nil {
		t.Fatal(err)
	}
	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithProviderRoutes(routes),
	)
	srv.readTimeout = bodyTimeout
	ctx, cancel := context.WithCancel(context.Background())
	if err := srv.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
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

func TestProxyServerTimeoutsSpareStreaming(t *testing.T) {
	srv := startTimeoutProxy(t, "http://127.0.0.1:1", time.Second)
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.httpSrv.IdleTimeout <= 0 {
		t.Error("IdleTimeout is unset: idle keep-alive connections are held forever")
	}
	if srv.httpSrv.ReadTimeout <= 0 {
		t.Error("ReadTimeout is unset: a client trickling a request body holds the connection forever")
	}
	if srv.httpSrv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout=%v: a connection-wide write deadline cuts long streaming replies", srv.httpSrv.WriteTimeout)
	}
}

// TestProxyCutsOffSlowRequestBody pins that a client trickling a request
// body is disconnected once the read timeout passes.
func TestProxyCutsOffSlowRequestBody(t *testing.T) {
	var complete atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b, err := io.ReadAll(r.Body); err == nil && len(b) == 100 {
			complete.Store(true)
		}
	}))
	defer upstream.Close()
	srv := startTimeoutProxy(t, upstream.URL, 200*time.Millisecond)

	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST /openai/v1/chat/completions HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"model\":", srv.Addr())

	// Keep the request open far past the deadline, sending nothing more.
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	start := time.Now()
	_, err = io.ReadAll(conn)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("slow body held the connection %v (err %v); want a cut-off near the 200ms deadline", elapsed, err)
	}
	if complete.Load() {
		t.Error("upstream received a complete body")
	}
}

// TestProxyStreamsPastBodyDeadline pins that the read timeout does not
// limit how long a reply may stream once the request has been read, for a
// request with a body and one without.
func TestProxyStreamsPastBodyDeadline(t *testing.T) {
	const events = 6
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for i := range events {
			fmt.Fprintf(w, "data: %d\n\n", i)
			flusher.Flush()
			time.Sleep(100 * time.Millisecond)
		}
	}))
	defer upstream.Close()
	srv := startTimeoutProxy(t, upstream.URL, 150*time.Millisecond)

	for _, body := range []string{`{"model":"gpt-4o-mini","stream":true}`, ""} {
		method := http.MethodPost
		if body == "" {
			method = http.MethodGet
		}
		req, _ := http.NewRequest(method, "http://"+srv.Addr()+"/openai/v1/chat/completions", strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var got int
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data: ") {
				got++
			}
		}
		_ = resp.Body.Close()
		if got != events {
			t.Errorf("%s: received %d of %d events (scan err %v): the stream was cut", method, got, events, sc.Err())
		}
	}
}
