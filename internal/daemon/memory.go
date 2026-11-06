package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime/debug"
	"runtime/metrics"
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
	stop := make(chan struct{})
	go tuneMemoryLimit(stop, memoryTuneEvery, liveHeap, s.logger)
	s.deferCleanup(func() { close(stop) })
	return nil
}

// memoryTuneEvery is how often the limit is matched to the live heap.
const memoryTuneEvery = 30 * time.Second

// memoryLimitFor is the soft limit for a live heap: the default, or half
// again the live heap when that is more. The limit trims the collector's
// headroom (without one the heap doubles before a collection); below the
// live heap it leaves none, and the collector runs back to back, capped
// at half the CPU. A month of a busy machine's events outgrew the fixed
// default (1.3 GB live), and that held the daemon at two cores.
func memoryLimitFor(live uint64) int64 {
	return max(int64(defaultMemoryLimit), int64(live+live/2))
}

// applyMemoryLimit sets the limit for live when it differs from the
// current one by a tenth or more, and returns the limit in force.
func applyMemoryLimit(live uint64, logger *slog.Logger) int64 {
	want := memoryLimitFor(live)
	cur := debug.SetMemoryLimit(-1)
	diff := want - cur
	if diff < 0 {
		diff = -diff
	}
	if diff < cur/10 {
		return cur
	}
	debug.SetMemoryLimit(want)
	logger.Info("memory limit follows the live heap", "live_bytes", live, "limit_bytes", want)
	return want
}

// tuneMemoryLimit matches the limit to the live heap every period until
// stop closes.
func tuneMemoryLimit(stop <-chan struct{}, every time.Duration, live func() uint64, logger *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			applyMemoryLimit(live(), logger)
		}
	}
}

// liveHeap is the heap the last collection found live.
func liveHeap() uint64 {
	sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(sample)
	if sample[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return sample[0].Value.Uint64()
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
