package proxy

import (
	"fmt"
	"net/http"
	"time"

	"github.com/felixgeelhaar/fortify/circuitbreaker"
	fortifyhttp "github.com/felixgeelhaar/fortify/http"
	"github.com/felixgeelhaar/fortify/streamtimeout"
)

// ResilienceConfig wraps each provider proxy route with a fortify
// CircuitBreakerStream so long-lived LLM streams get per-chunk health
// signals plus a per-provider circuit breaker that trips on
// consecutive upstream failures.
//
// Off by default; install via WithResilience. Each provider route
// gets its own *circuitbreaker.CircuitBreaker so a flaky vendor
// doesn't take the proxy offline for other vendors.
type ResilienceConfig struct {
	FirstByteTimeout time.Duration
	IdleTimeout      time.Duration
	TotalTimeout     time.Duration
	// FailureThreshold is the consecutive-failure count that trips
	// the breaker. Defaults to 5 when zero.
	FailureThreshold uint32
}

// WithResilience enables fortify's CircuitBreakerStream around every
// provider route. Pass a zero-valued config (or omit the option) to
// keep the legacy behaviour where the proxy passes streams through
// unmodified.
func WithResilience(cfg ResilienceConfig) Option {
	return func(s *Server) {
		s.resilience = &cfg
	}
}

// resilienceMiddleware constructs a per-provider CircuitBreakerStream
// middleware. Returns nil when no resilience config is set so the
// caller can short-circuit and avoid an unnecessary wrap.
func (s *Server) resilienceMiddleware(route ProviderRoute) (func(http.Handler) http.Handler, error) {
	if s.resilience == nil {
		return nil, nil
	}
	threshold := s.resilience.FailureThreshold
	if threshold == 0 {
		threshold = 5
	}
	// Per-provider breaker so a flaky vendor doesn't trip routes for
	// other vendors. Provider ID gets attributed via s.logger when the
	// breaker fires, since circuitbreaker.Config has no Name field.
	cb := circuitbreaker.New[*http.Response](circuitbreaker.Config{ //nolint:bodyclose // generic constructor, not an HTTP call
		MaxRequests: 100,
		Interval:    time.Minute,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(c circuitbreaker.Counts) bool {
			return c.ConsecutiveFailures >= threshold
		},
	})
	mw, err := fortifyhttp.CircuitBreakerStream(cb, streamtimeout.Config{
		FirstByteTimeout: s.resilience.FirstByteTimeout,
		IdleTimeout:      s.resilience.IdleTimeout,
		TotalTimeout:     s.resilience.TotalTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("provider %s: %w", route.Provider.ID, err)
	}
	return mw, nil
}
