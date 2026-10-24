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
	"slices"
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

// TestProxyForwardsBodyAfterReplyStarts pins that a provider route keeps
// the request body readable once the reply has started. An HTTP/1 server
// otherwise consumes and closes any unread request body as the reply's
// header is written, while the reverse proxy's transport may still be
// reading that body in its own goroutine: its next read fails, it closes
// the upstream connection, and the stream is cut after the first event.
// TestProxyStreamsPastBodyDeadline lost that race on CI when the
// transport's end-of-body check ran after the header was written; here
// the upstream answers before it reads the body, so the outcome does not
// depend on scheduling.
func TestProxyForwardsBodyAfterReplyStarts(t *testing.T) {
	const payload = `{"model":"gpt-4o-mini","stream":true}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The upstream, a Go server too, must not discard the body itself.
		if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: started\n\n")
		w.(http.Flusher).Flush()
		b, err := io.ReadAll(r.Body)
		if err != nil || string(b) != payload {
			fmt.Fprintf(w, "data: body %q, err %v\n\n", b, err)
			return
		}
		fmt.Fprint(w, "data: body-ok\n\n")
	}))
	defer upstream.Close()
	// The body arrives only after the first event; the read timeout ends
	// the request if the proxy never lets it through.
	srv := startTimeoutProxy(t, upstream.URL, 2*time.Second)

	pr, pw := io.Pipe()
	req, _ := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/openai/v1/chat/completions", pr)
	req.ContentLength = int64(len(payload))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var events []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		events = append(events, data)
		if len(events) == 1 {
			// Send the body only once the reply has started.
			go func() {
				_, _ = io.WriteString(pw, payload)
				_ = pw.Close()
			}()
		}
	}
	if want := []string{"started", "body-ok"}; !slices.Equal(events, want) {
		t.Errorf("events %q (scan err %v), want %q: the request body did not reach the upstream once the reply had started", events, sc.Err(), want)
	}
}
