package proxy

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// StreamMeter observes upstream response bodies as they pass through the
// proxy. The proxy invokes Observe for every chunk read from the upstream
// (which, for SSE, is roughly one event per call) and Done with the final
// byte count once the body is closed. Implementations must be safe for
// concurrent use across requests; per-request state should be created via
// NewMeter so the proxy can fan out one fresh meter per stream.
type StreamMeter interface {
	// NewMeter is invoked once per upstream response. It returns the per-
	// request meter that will receive Observe / Done callbacks.
	NewMeter(resp *http.Response) RequestMeter
}

// RequestMeter is the per-stream half of the StreamMeter contract.
type RequestMeter interface {
	// Observe is called with each chunk read from the upstream body. The
	// slice is reused after the call returns; copy what you need.
	Observe(chunk []byte)
	// Done is called exactly once when the body is fully consumed (or
	// closed). totalBytes is the cumulative byte count.
	Done(totalBytes int64)
}

// noopMeter is the default when WithStreamMeter is not configured. It
// avoids nil checks at every call site.
type noopMeter struct{}

func (noopMeter) NewMeter(*http.Response) RequestMeter { return noopRequestMeter{} }

type noopRequestMeter struct{}

func (noopRequestMeter) Observe([]byte) {}
func (noopRequestMeter) Done(int64)     {}

// WithStreamMeter installs a meter that observes every upstream response.
// Pass nil (or omit the option) to disable metering.
func WithStreamMeter(m StreamMeter) Option {
	return func(s *Server) {
		if m == nil {
			s.streamMeter = noopMeter{}
			return
		}
		s.streamMeter = m
	}
}

// isStreamingResponse reports whether resp is an SSE stream that the proxy
// should flush per-chunk. Detection is content-type based: "text/event-
// stream" is the canonical media type; chunked Transfer-Encoding alone is
// not sufficient because plain JSON responses are often chunked too.
func isStreamingResponse(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	ct := resp.Header.Get("Content-Type")
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "text/event-stream")
}

// meteredBody wraps an upstream response body so the proxy can observe
// chunks and the final total without buffering. Read forwards to the
// underlying ReadCloser, calls meter.Observe for each non-empty chunk,
// and meter.Done is invoked at most once on EOF or Close.
type meteredBody struct {
	rc    io.ReadCloser
	meter RequestMeter
	total atomic.Int64
	done  atomic.Bool
}

func newMeteredBody(rc io.ReadCloser, m RequestMeter) *meteredBody {
	return &meteredBody{rc: rc, meter: m}
}

func (b *meteredBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.total.Add(int64(n))
		b.meter.Observe(p[:n])
	}
	if err == io.EOF {
		b.markDone()
	}
	return n, err
}

func (b *meteredBody) Close() error {
	b.markDone()
	return b.rc.Close()
}

func (b *meteredBody) markDone() {
	if b.done.CompareAndSwap(false, true) {
		b.meter.Done(b.total.Load())
	}
}
