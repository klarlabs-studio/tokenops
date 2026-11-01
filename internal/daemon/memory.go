package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime/debug"
	"time"
)

// defaultMemoryLimit is the daemon's soft memory limit when GOMEMLIMIT is
// not set. Without one the Go heap may double before a collection, and a
// daemon that reads weeks of events held 1.5 to 2.3 GB on a busy machine
// (#825). It is soft: the collector works harder near it, it never stops
// the daemon.
const defaultMemoryLimit = 1 << 30

// memoryLimitEnv and profilerEnv are the environment the daemon reads.
const (
	memoryLimitEnv = "GOMEMLIMIT"
	profilerEnv    = "TOKENOPS_PPROF_ADDR"
)

// limitMemory sets the soft memory limit, leaving one set through
// GOMEMLIMIT as it is.
func (s *startup) limitMemory(context.Context) error {
	if os.Getenv(memoryLimitEnv) != "" {
		s.logger.Info("memory limit from GOMEMLIMIT", "limit_bytes", debug.SetMemoryLimit(-1))
		return nil
	}
	debug.SetMemoryLimit(defaultMemoryLimit)
	return nil
}

// startProfiler serves Go's profiles on TOKENOPS_PPROF_ADDR when it is
// set, on a loopback address only: a profile shows what the daemon holds,
// and it is for diagnosing it on the machine it runs on.
func (s *startup) startProfiler(ctx context.Context) error {
	addr := os.Getenv(profilerEnv)
	if addr == "" {
		return nil
	}
	if err := loopbackOnly(addr); err != nil {
		return fmt.Errorf("%s: %w", profilerEnv, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("%s: %w", profilerEnv, err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Warn("profiler stopped", "err", err)
		}
	}()
	s.deferCleanup(func() { _ = srv.Close() })
	s.logger.Info("profiler listening", "addr", ln.Addr().String())
	return nil
}

// loopbackOnly refuses an address that is not a loopback IP or localhost.
func loopbackOnly(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%q is not a loopback address; use 127.0.0.1:<port>", addr)
}
