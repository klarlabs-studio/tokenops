package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/tokenizer"
	"github.com/felixgeelhaar/tokenops/internal/events"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// benchNoopSink discards every batch instantly so the bus worker never
// blocks. Using this with events.NewAsync exercises the full publish path
// (queue, batch, flush) without measuring storage latency.
type benchNoopSink struct{}

func (benchNoopSink) AppendBatch(context.Context, []*eventschema.Envelope) error { return nil }

func newBenchBus() *events.AsyncBus {
	return events.NewAsync(benchNoopSink{}, events.Options{
		QueueCapacity: 4096,
		BatchSize:     128,
		BatchWait:     50 * time.Millisecond,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// proxy-bench: harness to validate that the TokenOps proxy adds <50ms p99
// overhead over a direct upstream call. The mock upstream below answers
// instantly so the measured delta is the proxy's own cost (capture, hash,
// tokenizer, forwarding, observer publish).

var benchPayload = []byte(`{"model":"gpt-4o-mini","messages":[{"role":"system","content":"You are a helpful assistant."},{"role":"user","content":"Summarise the quick brown fox in three sentences."}]}`)

var benchResponseJSON = []byte(`{"id":"chatcmpl-bench","object":"chat.completion","model":"gpt-4o-mini","usage":{"prompt_tokens":42,"completion_tokens":24,"total_tokens":66},"choices":[{"index":0,"message":{"role":"assistant","content":"The quick brown fox jumps over the lazy dog. It is a classic pangram. It contains every letter of the English alphabet."}}]}`)

// newBenchUpstream returns a tiny httptest server that answers with a fixed
// JSON body. Latency is the localhost socket cost only.
func newBenchUpstream(tb testing.TB) *httptest.Server {
	tb.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(benchResponseJSON)
	}))
	tb.Cleanup(srv.Close)
	return srv
}

// newBenchSSEUpstream emits a small SSE stream of n events.
func newBenchSSEUpstream(tb testing.TB, events int) *httptest.Server {
	tb.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		for i := 0; i < events; i++ {
			_, _ = fmt.Fprintf(w, "data: {\"delta\":\"chunk-%d\"}\n\n", i)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	tb.Cleanup(srv.Close)
	return srv
}

func startBenchProxy(tb testing.TB, opts ...Option) (*Server, func()) {
	tb.Helper()
	srv := New("127.0.0.1:0",
		append([]Option{
			WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
			WithShutdownTimeout(time.Second),
		}, opts...)...,
	)
	ctx, cancel := context.WithCancel(context.Background())
	if err := srv.Start(ctx); err != nil {
		cancel()
		tb.Fatalf("Start: %v", err)
	}
	waitListening(&testing.T{}, srv.Addr()) // best effort
	stop := func() {
		cancel()
		shutdownCtx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	}
	tb.Cleanup(stop)
	return srv, stop
}

func benchClient() *http.Client {
	tr := &http.Transport{
		MaxIdleConns:        128,
		MaxIdleConnsPerHost: 128,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  true,
	}
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}

func doBenchPost(b *testing.B, c *http.Client, url string) {
	b.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(benchPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-bench")
	resp, err := c.Do(req)
	if err != nil {
		b.Fatalf("do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// BenchmarkProxyForwardBaseline measures the cost of a localhost POST to the
// upstream directly. Used as the floor for proxy-overhead reporting.
func BenchmarkProxyForwardBaseline(b *testing.B) {
	up := newBenchUpstream(b)
	c := benchClient()
	url := up.URL + "/v1/chat/completions"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		doBenchPost(b, c, url)
	}
}

// BenchmarkProxyForward measures the cost of going through the proxy with no
// observer wired in. This is the lower-bound proxy overhead.
func BenchmarkProxyForward(b *testing.B) {
	up := newBenchUpstream(b)
	routes, err := BuildProviderRoutes(map[string]string{"openai": up.URL})
	if err != nil {
		b.Fatalf("routes: %v", err)
	}
	srv, _ := startBenchProxy(b, WithProviderRoutes(routes))
	c := benchClient()
	url := "http://" + srv.Addr() + "/openai/v1/chat/completions"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		doBenchPost(b, c, url)
	}
}

// BenchmarkProxyForwardObserver measures proxy cost with the full observer
// pipeline (capture, hash, tokenize, publish) wired in. This is the
// production-realistic configuration.
func BenchmarkProxyForwardObserver(b *testing.B) {
	up := newBenchUpstream(b)
	routes, err := BuildProviderRoutes(map[string]string{"openai": up.URL})
	if err != nil {
		b.Fatalf("routes: %v", err)
	}
	bus := newBenchBus()
	defer bus.Close(time.Second)
	reg := tokenizer.NewRegistry()

	srv, _ := startBenchProxy(b,
		WithProviderRoutes(routes),
		WithEventBus(bus),
		WithTokenizer(reg),
	)
	c := benchClient()
	url := "http://" + srv.Addr() + "/openai/v1/chat/completions"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		doBenchPost(b, c, url)
	}
	b.StopTimer()
}

// BenchmarkProxySSE measures proxy cost on a small streaming response with
// the full observer pipeline. Validates that flush + meter does not add
// disproportionate latency.
func BenchmarkProxySSE(b *testing.B) {
	up := newBenchSSEUpstream(b, 8)
	routes, err := BuildProviderRoutes(map[string]string{"openai": up.URL})
	if err != nil {
		b.Fatalf("routes: %v", err)
	}
	bus := newBenchBus()
	defer bus.Close(time.Second)
	reg := tokenizer.NewRegistry()

	srv, _ := startBenchProxy(b,
		WithProviderRoutes(routes),
		WithEventBus(bus),
		WithTokenizer(reg),
	)
	c := benchClient()
	url := "http://" + srv.Addr() + "/openai/v1/chat/completions"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		doBenchPost(b, c, url)
	}
	b.StopTimer()
}

// TestProxyP99OverheadGate is the CI regression gate for the proxy-bench
// task. It runs a paired-sample latency probe (direct upstream vs
// through-proxy with the full observer wired in) and asserts the proxy's
// p99 overhead stays below 50 ms. The threshold target comes from the
// product brief; the loose bound (override via TOKENOPS_BENCH_P99_MS) is
// for noisy CI runners.
//
// Skipped under -short so unit-test runs stay fast.
func TestProxyP99OverheadGate(t *testing.T) {
	if testing.Short() {
		t.Skip("p99 overhead gate runs without -short")
	}

	const samples = 200
	const warmup = 20

	threshold := 50 * time.Millisecond
	if v := os.Getenv("TOKENOPS_BENCH_P99_MS"); v != "" {
		var ms int
		if _, err := fmt.Sscanf(v, "%d", &ms); err == nil && ms > 0 {
			threshold = time.Duration(ms) * time.Millisecond
		}
	}

	up := newBenchUpstream(t)
	routes, err := BuildProviderRoutes(map[string]string{"openai": up.URL})
	if err != nil {
		t.Fatalf("routes: %v", err)
	}
	bus := newBenchBus()
	defer bus.Close(time.Second)
	reg := tokenizer.NewRegistry()
	srv, _ := startBenchProxy(t,
		WithProviderRoutes(routes),
		WithEventBus(bus),
		WithTokenizer(reg),
	)

	c := benchClient()
	directURL := up.URL + "/v1/chat/completions"
	proxyURL := "http://" + srv.Addr() + "/openai/v1/chat/completions"

	// Warm both paths so first-request connection setup does not skew the
	// p99 measurement.
	for i := 0; i < warmup; i++ {
		measureOnce(t, c, directURL)
		measureOnce(t, c, proxyURL)
	}

	direct := make([]time.Duration, samples)
	proxy := make([]time.Duration, samples)
	for i := 0; i < samples; i++ {
		direct[i] = measureOnce(t, c, directURL)
		proxy[i] = measureOnce(t, c, proxyURL)
	}

	directP50 := percentile(direct, 0.50)
	directP99 := percentile(direct, 0.99)
	proxyP50 := percentile(proxy, 0.50)
	proxyP99 := percentile(proxy, 0.99)
	overheadP50 := proxyP50 - directP50
	overheadP99 := proxyP99 - directP99

	t.Logf("direct  p50=%s p99=%s", directP50, directP99)
	t.Logf("proxy   p50=%s p99=%s", proxyP50, proxyP99)
	t.Logf("overhead p50=%s p99=%s threshold=%s", overheadP50, overheadP99, threshold)

	// Negative deltas can occur on noisy runners (httptest direct path
	// momentarily slower than proxy in a single sample); only fail when
	// proxy is meaningfully slower than the threshold.
	if overheadP99 > threshold {
		t.Fatalf("proxy p99 overhead %s exceeds threshold %s (direct p99=%s, proxy p99=%s)",
			overheadP99, threshold, directP99, proxyP99)
	}
}

func measureOnce(tb testing.TB, c *http.Client, url string) time.Duration {
	tb.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(benchPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-bench")
	start := time.Now()
	resp, err := c.Do(req)
	if err != nil {
		tb.Fatalf("do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return time.Since(start)
}

func percentile(samples []time.Duration, p float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	dup := append([]time.Duration(nil), samples...)
	sort.Slice(dup, func(i, j int) bool { return dup[i] < dup[j] })
	idx := int(float64(len(dup)-1) * p)
	return dup[idx]
}
