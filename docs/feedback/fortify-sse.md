# fortify feedback: first-class SSE support in `fortify/http`

**Source repo:** felixgeelhaar/fortify v1.4.0
**Reporter:** TokenOps integration (felixgeelhaar/tokenops)
**Date:** 2026-05-12

## Summary

fortify's HTTP middleware (`fortify/http`) and the underlying generic
`circuitbreaker.CircuitBreaker[*http.Response]` collapse a streaming
response into a single observation: success/failure is only decided
after `next.ServeHTTP` returns. That model works for unary
request/response, but breaks SSE and chunked responses on two axes:

1. **Flushing is lost.** `responseRecorder` (used by both
   `http.CircuitBreaker` and `http.Timeout`) wraps `http.ResponseWriter`
   without forwarding `http.Flusher`/`http.Hijacker`/`http.Pusher`. SSE
   clients depend on per-event `Flush()`; the type assertion in the
   handler (or in `httputil.ReverseProxy`'s `FlushInterval`) fails
   silently, so each chunk is buffered until the handler returns.
2. **Failure signal is wrong.** For a long-lived stream, the breaker
   cannot distinguish "healthy long stream" from "stalled connection".
   Today the breaker waits for ServeHTTP to return — by which point the
   client has either received the whole stream or already dropped.
   Per-event errors (upstream reset mid-stream, malformed SSE frame)
   are invisible to the breaker until the connection closes.

fortify already ships `streamtimeout` for exactly this domain — what's
missing is the HTTP middleware glue and a breaker variant that consumes
per-chunk signals.

## Observed in TokenOps

We adopted fortify's circuit breaker for the OTLP HTTP exporter (finite
unary call — works perfectly) but had to roll back from our provider
proxy routes. The proxy fronts SSE streams from OpenAI / Anthropic /
Gemini; wrapping their handlers with `fortify/http.CircuitBreaker`
caused:

- the dashboard's live stream stalling for ~tens of seconds, then
  delivering all events in one burst when the upstream closed the
  connection,
- `X-Accel-Buffering: no` (which we set on responses) being effectively
  overridden because the buffering happened in our own middleware, not
  the proxy,
- the breaker reporting the route as healthy even when the upstream
  truncated the stream after the first event.

## Concrete proposals

### 1. `responseRecorder` should forward streaming interfaces

```go
func (r *responseRecorder) Flush() {
    if f, ok := r.ResponseWriter.(http.Flusher); ok {
        f.Flush()
    }
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
    if h, ok := r.ResponseWriter.(http.Hijacker); ok {
        return h.Hijack()
    }
    return nil, nil, http.ErrNotSupported
}
```

Even better: don't expose the recorder unconditionally — return a
writer that implements only the same optional interfaces as the
underlying writer, so callers that do `_, ok := w.(http.Flusher)` get
the truthful answer. The `httpsnoop` package does this with a
generated switch over the 2^N interface combinations; it's a one-time
cost that eliminates a whole class of middleware bugs.

### 2. Streaming-aware breaker mode

Add a sibling middleware that:

- detects `Content-Type: text/event-stream` (or `Transfer-Encoding: chunked`)
  on the response header — at `WriteHeader(...)` time, **before** the
  first chunk goes out — and switches to streaming mode,
- in streaming mode, defers the breaker decision: instead of treating
  ServeHTTP-returns as the success/failure boundary, expose a chunk
  callback (`OnChunk(ctx, n int, err error)`) that lets the caller
  feed health signals into the breaker per-frame.

Sketch:

```go
type StreamingCircuitBreaker struct {
    breaker circuitbreaker.CircuitBreaker[*http.Response]
    // OnChunk reports each chunk's outcome. Return true to keep
    // streaming, false to abort and trip the breaker.
    OnChunk func(ctx context.Context, n int, err error) bool
}
```

For pure SSE use cases without per-event semantics, a simpler default
would be: "if the first chunk arrives within FirstByteTimeout and the
stream produces at least one chunk every IdleTimeout, the call counts
as success" — which is exactly `streamtimeout`'s contract. The
middleware can construct an internal `streamtimeout.StreamTimeout` and
treat *its* terminal error as the breaker's input.

### 3. Compose with `streamtimeout` directly

A drop-in helper would make the recommended pattern obvious:

```go
// CircuitBreakerStream wires a CircuitBreaker into an SSE/chunked
// response pipeline. Per-chunk Mark() calls satisfy the streamtimeout
// watchdogs; a fired watchdog trips the breaker.
func CircuitBreakerStream(
    cb circuitbreaker.CircuitBreaker[*http.Response],
    cfg streamtimeout.Config,
) func(http.Handler) http.Handler
```

Internally this can use a `flushAwareWriter` that calls Mark on every
Write, plus a goroutine that watches the streamtimeout's error channel
and (a) writes a 504 if no chunks have been emitted yet, or (b) closes
the connection via Hijack if the headers are already flushed.

### 4. Documentation: a "streaming" page

Add `docs/streaming.md` to the fortify docs site explaining:
- which patterns are safe for streaming (`streamtimeout`,
  `CircuitBreakerStream` once added),
- which patterns must be avoided (`fortify/http.Timeout` with a
  duration shorter than the expected stream length — currently it
  silently truncates),
- a worked example: SSE proxy with `httputil.ReverseProxy`,
  `FlushInterval: -1`, fortify retry on the *handshake* (headers
  received), and streamtimeout on the *body*.

## Testing checklist

A regression test that would catch the buffering bug today:

```go
func TestCircuitBreakerPreservesFlusher(t *testing.T) {
    var got int
    h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        f, ok := w.(http.Flusher)
        if !ok { t.Fatal("Flusher lost through middleware") }
        for i := 0; i < 3; i++ {
            fmt.Fprintf(w, "data: %d\n\n", i)
            f.Flush()
            got++
        }
    })
    cb := circuitbreaker.New[*http.Response](/* ... */)
    srv := httptest.NewServer(fortifyhttp.CircuitBreaker(cb)(h))
    defer srv.Close()
    // Read with a 100ms TTFB budget — passes only if chunks flush.
    ...
}
```

## Suggested rollout

- v1.5: `responseRecorder` forwards `Flusher`/`Hijacker`/`Pusher`
  (small, backwards-compatible fix; unlocks SSE for existing CB users).
- v1.5: new `streaming.go` package with `CircuitBreakerStream`
  composing `circuitbreaker` + `streamtimeout`.
- v1.6: deprecate `fortify/http.Timeout` for streaming bodies (it
  still applies to handshake); recommend `streamtimeout` in the
  godoc.

## Related code in fortify

- `http/middleware.go:50` — `CircuitBreaker` middleware
- `http/middleware.go:108` — `Timeout` middleware (same buffering bug)
- `http/middleware.go:328` — `responseRecorder` (missing Flusher)
- `streamtimeout/streamtimeout.go` — the streaming primitive that
  should anchor the SSE story
