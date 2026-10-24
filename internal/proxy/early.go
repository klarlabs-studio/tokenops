package proxy

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// bootHoldTimeout bounds how long a provider request that arrives while
// the daemon boots waits for the proxy before it is answered 503.
const bootHoldTimeout = 30 * time.Second

// EarlyListener holds the daemon's address from the first moment of the
// boot, before the stores, readers and API the proxy serves are built.
//
// Until the server takes it over (Server.Start with WithEarlyListener) it
// answers the probes at once: /healthz is 200 with "state": "starting",
// /readyz is 503 "starting", /version names the build. The API answers
// 503 "starting" with Retry-After rather than waiting, so a caller can
// say the daemon is warming up; a provider request waits for the proxy,
// up to bootHoldTimeout, because a client's model call has nothing better
// to do than be served late.
//
// The boot used to bind the port only after everything else was ready.
// On a large store under start-up load that took 45s, and `daemon
// restart`, which waits 20s for /healthz, reported a daemon that was
// starting normally as one that "did not become healthy".
type EarlyListener struct {
	ln       net.Listener
	srv      *http.Server
	listenAt string
	// tlsCfg is the config TLS is served with, nil without TLS. It is
	// kept apart from srv.TLSConfig, which Serve writes to.
	tlsCfg *tls.Config
	logger *slog.Logger

	handler  atomic.Pointer[http.Handler]
	ready    chan struct{}
	readyOne sync.Once
	// handedOver is set once a Server owns the listener; Close then
	// leaves it to the Server's shutdown.
	handedOver atomic.Bool
	startedAt  time.Time
}

// ListenEarly binds addr and starts answering on it at once. tlsCfg, when
// non-nil, serves TLS from the first connection; allowedHosts are the
// Host names admitted beyond loopback, as WithAllowedHosts.
func ListenEarly(addr string, tlsCfg *tls.Config, allowedHosts []string, logger *slog.Logger) (*EarlyListener, error) {
	if logger == nil {
		logger = slog.Default()
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	e := &EarlyListener{
		ln: ln, listenAt: ln.Addr().String(), tlsCfg: tlsCfg, logger: logger,
		ready: make(chan struct{}), startedAt: time.Now(),
	}
	boot := newHostGuard(addr, allowedHosts).middleware(logger, http.HandlerFunc(e.serveBoot))
	e.srv = &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if h := e.handler.Load(); h != nil {
				(*h).ServeHTTP(w, r)
				return
			}
			boot.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       defaultReadTimeout,
		IdleTimeout:       defaultIdleTimeout,
		TLSConfig:         tlsCfg,
	}
	go func() {
		var serveErr error
		if tlsCfg != nil {
			serveErr = e.srv.ServeTLS(ln, "", "")
		} else {
			serveErr = e.srv.Serve(ln)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("proxy serve", "err", serveErr)
		}
	}()
	logger.Info("daemon listening while it starts", "addr", e.listenAt, "tls", tlsCfg != nil)
	return e, nil
}

// Addr is the bound address (":0" resolved to the real port).
func (e *EarlyListener) Addr() string { return e.listenAt }

// handOver installs the full handler, releases the requests waiting for
// it and returns the http.Server and listener the Server now owns.
func (e *EarlyListener) handOver(h http.Handler) (*http.Server, net.Listener) {
	e.handedOver.Store(true)
	e.handler.Store(&h)
	e.readyOne.Do(func() { close(e.ready) })
	return e.srv, e.ln
}

// Close stops serving when the boot fails before a Server took the
// listener over. After the hand-over it does nothing: the Server's
// shutdown drains the connections.
func (e *EarlyListener) Close() error {
	if e == nil || e.handedOver.Load() {
		return nil
	}
	err := e.srv.Close()
	// Serve may not have started tracking the listener yet; close it
	// here too so the address is free when Close returns.
	_ = e.ln.Close()
	return err
}

// serveBoot answers a request that arrived before the hand-over.
func (e *EarlyListener) serveBoot(w http.ResponseWriter, r *http.Request) {
	probe := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case probe && r.URL.Path == "/healthz":
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "ok",
			"state":      "starting",
			"started_at": startedAt.UTC(),
			"uptime_ns":  time.Since(startedAt).Nanoseconds(),
		})
		return
	case probe && r.URL.Path == "/readyz":
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":       "starting",
			"blockers":     []string{},
			"next_actions": []string{},
		})
		return
	case probe && r.URL.Path == "/version":
		versionHandler(w, r)
		return
	case r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/"):
		e.writeStarting(w)
		return
	}
	timer := time.NewTimer(bootHoldTimeout)
	defer timer.Stop()
	select {
	case <-e.ready:
		(*e.handler.Load()).ServeHTTP(w, r)
	case <-r.Context().Done():
	case <-timer.C:
		e.writeStarting(w)
	}
}

// writeStarting answers 503 with the reason and when to try again.
func (e *EarlyListener) writeStarting(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "2")
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"error":   "starting",
		"message": "the tokenops daemon is starting; try again in a few seconds",
		"uptime":  time.Since(e.startedAt).Round(time.Millisecond).String(),
	})
}

// WithEarlyListener has Start serve on a listener ListenEarly already
// bound, taking it over without dropping a connection, instead of binding
// the address itself.
func WithEarlyListener(e *EarlyListener) Option {
	return func(s *Server) { s.early = e }
}
