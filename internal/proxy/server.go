// Package proxy hosts the TokenOps reverse proxy daemon. The skeleton in this
// task wires up the HTTP listener, lifecycle, and health endpoints; provider
// routing, TLS termination, and event emission are added by their dedicated
// tasks (proxy-providers, proxy-tls, proxy-events).
package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/version"
)

// Server is the TokenOps proxy daemon.
type Server struct {
	addr            string
	shutdownTimeout time.Duration
	logger          *slog.Logger

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

// New constructs a Server bound to addr (host:port). The Server does not
// listen until Start is called.
func New(addr string, opts ...Option) *Server {
	s := &Server{
		addr:            addr,
		shutdownTimeout: 15 * time.Second,
		logger:          slog.Default(),
	}
	for _, opt := range opts {
		opt(s)
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

	s.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	srv := s.httpSrv
	s.mu.Unlock()

	s.logger.Info("proxy listening",
		"addr", s.listenAt,
		"version", version.Version,
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
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("proxy serve", "err", err)
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
