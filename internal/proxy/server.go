// Package proxy hosts the TokenOps reverse proxy daemon. The skeleton in this
// task wires up the HTTP listener, lifecycle, and health endpoints; provider
// routing, TLS termination, and event emission are added by their dedicated
// tasks (proxy-providers, proxy-tls, proxy-events).
package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/events"
	"github.com/felixgeelhaar/tokenops/internal/proxy/cache"
	"github.com/felixgeelhaar/tokenops/internal/tokenizer"
	"github.com/felixgeelhaar/tokenops/internal/version"
)

// Server is the TokenOps proxy daemon.
type Server struct {
	addr            string
	shutdownTimeout time.Duration
	logger          *slog.Logger
	routes          []ProviderRoute
	tlsConfig       *tls.Config
	streamMeter     StreamMeter
	bus             events.Bus
	tokenizer       *tokenizer.Registry
	source          string
	observerActive  bool
	cache           *cache.Cache
	analytics       *AnalyticsHandlers

	mu       sync.Mutex
	httpSrv  *http.Server
	listener net.Listener
	listenAt string
}

// Option mutates a Server during construction.
type Option func(*Server)

// WithLogger attaches a structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(s *Server) { s.logger = l }
}

// WithShutdownTimeout overrides the default 15s graceful-shutdown timeout.
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *Server) { s.shutdownTimeout = d }
}

// WithProviderRoutes installs the upstream LLM provider routes the proxy
// should mount. When omitted, the daemon serves only its control endpoints.
func WithProviderRoutes(routes []ProviderRoute) Option {
	return func(s *Server) { s.routes = routes }
}

// WithTLS configures the server to terminate TLS using cfg. When set,
// Start uses ServeTLS; clients must trust the certificate (e.g. via the
// CA pool minted by internal/tlsmint).
func WithTLS(cfg *tls.Config) Option {
	return func(s *Server) { s.tlsConfig = cfg }
}

// WithEventBus enables prompt event emission. Every request flowing
// through a provider route is captured: body hashed and tokenised,
// response status + streamed bytes metered, and a PromptEvent envelope
// pushed to the bus when the body closes.
func WithEventBus(b events.Bus) Option {
	return func(s *Server) {
		if b == nil {
			return
		}
		s.bus = b
		s.observerActive = true
	}
}

// WithTokenizer installs the tokenizer registry used by the request
// observer for input and output token estimation. Pass nil (or omit) to
// disable estimation; the observer still emits events with zero counts.
func WithTokenizer(reg *tokenizer.Registry) Option {
	return func(s *Server) { s.tokenizer = reg }
}

// WithCache installs a response cache in front of the provider routes.
// Cache hits are served without contacting upstream and emit a synthetic
// PromptEvent with CacheHit=true. Streaming responses are never cached.
// Pass nil to disable caching (the default).
func WithCache(c *cache.Cache) Option {
	return func(s *Server) { s.cache = c }
}

// WithSource overrides the Source field stamped on emitted envelopes.
// Defaults to "proxy".
func WithSource(src string) Option {
	return func(s *Server) {
		if src != "" {
			s.source = src
		}
	}
}

// TLSEnabled reports whether the server was constructed with WithTLS.
func (s *Server) TLSEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tlsConfig != nil
}

// New constructs a Server bound to addr (host:port). The Server does not
// listen until Start is called.
func New(addr string, opts ...Option) *Server {
	s := &Server{
		addr:            addr,
		shutdownTimeout: 15 * time.Second,
		logger:          slog.Default(),
		streamMeter:     noopMeter{},
		source:          "proxy",
	}
	for _, opt := range opts {
		opt(s)
	}
	// When the observer is active, replace the StreamMeter with one that
	// builds PromptEvents and publishes to the bus. Callers can still
	// override with WithStreamMeter for custom integrations by listing
	// it after WithEventBus.
	if s.observerActive {
		s.streamMeter = &observerMeter{
			bus:       s.bus,
			tokenizer: s.tokenizer,
			source:    s.source,
		}
	}
	return s
}

// Addr returns the resolved listener address. Before Start it returns the
// configured addr; after Start it returns the actual bound address (which
// resolves :0 to a real port for tests).
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listenAt != "" {
		return s.listenAt
	}
	return s.addr
}

// Start begins serving. It returns when the server is fully listening or an
// error occurred during bind/build. The server keeps running until ctx is
// cancelled or Shutdown is called explicitly; the goroutine that runs Serve
// is owned by Start, and its terminal error (if any other than
// http.ErrServerClosed) is returned via the channel returned by Done.
//
// Start is safe to call exactly once per Server.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.httpSrv != nil {
		s.mu.Unlock()
		return errors.New("proxy: server already started")
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.listener = ln
	s.listenAt = ln.Addr().String()

	mux := http.NewServeMux()
	s.registerRoutes(mux)
	if s.analytics != nil {
		s.analytics.Register(mux)
	}
	s.registerProviderRoutes(mux)

	s.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         s.tlsConfig,
	}
	srv := s.httpSrv
	tlsCfg := s.tlsConfig
	s.mu.Unlock()

	s.logger.Info("proxy listening",
		"addr", s.listenAt,
		"version", version.Version,
		"tls", tlsCfg != nil,
	)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("proxy shutdown", "err", err)
		}
	}()

	go func() {
		var serveErr error
		if tlsCfg != nil {
			// Certificates are already in tlsCfg; ServeTLS accepts empty
			// cert/key paths in that case.
			serveErr = srv.ServeTLS(ln, "", "")
		} else {
			serveErr = srv.Serve(ln)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.logger.Error("proxy serve", "err", serveErr)
		}
	}()

	return nil
}

// Shutdown initiates a graceful stop. It is safe to call after the context
// passed to Start has been cancelled; subsequent calls are no-ops.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	srv := s.httpSrv
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}
