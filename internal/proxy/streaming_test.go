package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/providers"
)

// recordingMeter captures Observe and Done calls for assertions.
type recordingMeter struct {
	mu      sync.Mutex
	chunks  [][]byte
	total   int64
	doneAt  time.Time
	created int
}

func (m *recordingMeter) NewMeter(*http.Response) RequestMeter {
	m.mu.Lock()
	m.created++
	m.mu.Unlock()
	return &recordingRequest{parent: m}
}

func (m *recordingMeter) snapshot() ([][]byte, int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, len(m.chunks))
	for i, c := range m.chunks {
		out[i] = append([]byte(nil), c...)
	}
	return out, m.total, !m.doneAt.IsZero()
}

type recordingRequest struct{ parent *recordingMeter }

func (r *recordingRequest) Observe(chunk []byte) {
	cp := append([]byte(nil), chunk...)
	r.parent.mu.Lock()
	r.parent.chunks = append(r.parent.chunks, cp)
	r.parent.mu.Unlock()
}

func (r *recordingRequest) Done(total int64) {
	r.parent.mu.Lock()
	r.parent.total = total
	r.parent.doneAt = time.Now()
	r.parent.mu.Unlock()
}

// startProxyWithUpstream wires up the proxy with one provider routed at
// /upstream/ and returns the proxy base URL.
func startProxyWithUpstream(t *testing.T, upstream *httptest.Server, meter StreamMeter) string {
	t.Helper()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream: %v", err)
	}
	route := ProviderRoute{
		Provider: providers.Provider{
			ID:             "stream-test",
			Prefix:         "/stream/",
			DefaultBaseURL: upstream.URL,
		},
		Upstream: u,
	}
	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithProviderRoutes([]ProviderRoute{route}),
		WithStreamMeter(meter),
	)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	})
	waitListening(t, srv.Addr())
	return "http://" + srv.Addr()
}

func TestSSEChunksDeliveredImmediately(t *testing.T) {
	const numEvents = 5
	const eventDelay = 80 * time.Millisecond

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("upstream: ResponseWriter is not a Flusher")
			return
		}
		for i := 0; i < numEvents; i++ {
			fmt.Fprintf(w, "data: chunk-%d\n\n", i)
			flusher.Flush()
			time.Sleep(eventDelay)
		}
	}))
	defer upstream.Close()

	meter := &recordingMeter{}
	base := startProxyWithUpstream(t, upstream, meter)

	resp, err := http.Get(base + "/stream/anything")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	reader := bufio.NewReader(resp.Body)
	arrivals := make([]time.Time, 0, numEvents)
	start := time.Now()

	for len(arrivals) < numEvents {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v (after %d arrivals)", err, len(arrivals))
		}
		if strings.HasPrefix(line, "data: ") {
			arrivals = append(arrivals, time.Now())
		}
	}

	// If chunks were buffered the entire response would arrive ~together
	// after numEvents*eventDelay. With per-chunk flushing they arrive
	// staggered. Assert the first chunk arrives well before the entire
	// stream finishes, with a generous buffer.
	firstArrival := arrivals[0].Sub(start)
	lastArrival := arrivals[len(arrivals)-1].Sub(start)
	maxFirst := eventDelay * 2 // first chunk should be quick
	minSpread := eventDelay    // last must trail first
	if firstArrival > maxFirst {
		t.Errorf("first chunk arrived after %s (max %s) — buffering suspected",
			firstArrival, maxFirst)
	}
	if lastArrival-firstArrival < minSpread {
		t.Errorf("chunks arrived too close together (spread %s, want >= %s) — buffering suspected",
			lastArrival-firstArrival, minSpread)
	}

	// Drain the rest of the body so the upstream handler returns and our
	// meteredBody hits EOF / Close.
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, _, done := meter.snapshot(); done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	chunks, total, done := meter.snapshot()
	if !done {
		t.Errorf("meter Done not called")
	}
	if total <= 0 {
		t.Errorf("total bytes = %d, want > 0", total)
	}
	if len(chunks) == 0 {
		t.Errorf("meter saw no chunks")
	}
}

func TestNonStreamingResponseStillMetered(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"hello":"world"}`)
	}))
	defer upstream.Close()

	meter := &recordingMeter{}
	base := startProxyWithUpstream(t, upstream, meter)

	resp, err := http.Get(base + "/stream/json")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != `{"hello":"world"}` {
		t.Errorf("body = %q", body)
	}
	chunks, total, done := meter.snapshot()
	if !done || total != int64(len(body)) {
		t.Errorf("meter total=%d done=%v, want %d/true (chunks=%d)", total, done, len(body), len(chunks))
	}
}

func TestClientCancelPropagatesToUpstream(t *testing.T) {
	upstreamCtx := make(chan context.Context, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCtx <- r.Context()
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: first\n\n")
		flusher.Flush()
		// Block until our context is cancelled (which happens when the
		// proxy aborts because the client disconnected).
		<-r.Context().Done()
		// Drain so the test sees the upstream-side cancel cleanly.
	}))
	defer upstream.Close()

	base := startProxyWithUpstream(t, upstream, nil)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/stream/cancel", nil)
	if err != nil {
		t.Fatalf("req: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	// Read the first chunk to ensure the upstream handler is in-flight.
	// Short reads or EOF are fine; we just need the upstream to start.
	buf := make([]byte, 32)
	_, _ = resp.Body.Read(buf)

	cancel()
	_ = resp.Body.Close()

	select {
	case rc := <-upstreamCtx:
		// Wait briefly for the upstream context to register cancellation.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if rc.Err() != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("upstream context never cancelled")
	case <-time.After(2 * time.Second):
		t.Fatal("upstream never recorded its context")
	}
}

func TestIsStreamingResponse(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{"text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		{"  text/event-stream  ", true},
		{"application/json", false},
		{"text/plain", false},
		{"", false},
	}
	for _, tc := range cases {
		resp := &http.Response{Header: http.Header{}}
		resp.Header.Set("Content-Type", tc.header)
		if got := isStreamingResponse(resp); got != tc.want {
			t.Errorf("isStreamingResponse(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
	if got := isStreamingResponse(nil); got {
		t.Errorf("nil resp = true")
	}
}

func TestMeteredBodyDoneOnceOnEOF(t *testing.T) {
	body := io.NopCloser(strings.NewReader("hello world"))
	doneCount := atomic.Int64{}
	rm := &countingMeter{doneCount: &doneCount}
	mb := newMeteredBody(body, rm)
	out, err := io.ReadAll(mb)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(out) != "hello world" {
		t.Errorf("body lost: %q", out)
	}
	_ = mb.Close()
	if got := doneCount.Load(); got != 1 {
		t.Errorf("Done called %d times, want 1", got)
	}
}

type countingMeter struct {
	doneCount *atomic.Int64
}

func (c *countingMeter) Observe([]byte) {}
func (c *countingMeter) Done(int64)     { c.doneCount.Add(1) }
