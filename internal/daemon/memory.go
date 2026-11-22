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

// memoryLimitFloor is the daemon's soft memory limit while the live heap
// is small, when GOMEMLIMIT is not set. It is soft: the collector works
// harder near it, it never stops the daemon.
//
// It was 1 GiB while the daemon held a month of decoded events (1.3 GB
// live on a busy machine, #825, #888). With them packed the live heap is
// a few hundred MB, and a 1 GiB limit let the heap grow to twice it
// before each collection: most of the daemon's footprint.
const memoryLimitFloor = 256 << 20

// gcPercent is the heap growth before a collection, as GOGC: half again
// the live heap, not Go's default of twice it. It is what bounds the heap
// in steady work, and it follows a burst of live heap at once, where a
// limit follows it only at its next tuning.
const gcPercent = 50

// memoryLimitEnv, gcPercentEnv and profilerEnv are the environment the
// daemon reads.
const (
	memoryLimitEnv = "GOMEMLIMIT"
	gcPercentEnv   = "GOGC"
	profilerEnv    = "TOKENOPS_PPROF_ADDR"
)

// limitMemory sets the collector's growth and the soft memory limit,
// leaving one set through GOGC or GOMEMLIMIT as it is.
func (s *startup) limitMemory(context.Context) error {
	if os.Getenv(gcPercentEnv) == "" {
		debug.SetGCPercent(gcPercent)
	}
	if os.Getenv(memoryLimitEnv) != "" {
		s.logger.Info("memory limit from GOMEMLIMIT", "limit_bytes", debug.SetMemoryLimit(-1))
		return nil
	}
	debug.SetMemoryLimit(memoryLimitFloor)
	stop := make(chan struct{})
	go tuneMemoryLimit(stop, memoryTuneEvery, liveHeap, s.logger)
	s.deferCleanup(func() { close(stop) })
	return nil
}

// memoryTuneEvery is how often the limit is matched to the live heap.
const memoryTuneEvery = 15 * time.Second

// memoryLimitFor is the soft limit for a live heap: twice it, or the
// floor when that is more. The collector's own target (gcPercent) is half
// again the live heap, so the limit stays clear of it and never binds in
// steady work. Below the live heap a limit leaves no headroom and the
// collector runs back to back, capped at half the CPU: a month of a busy
// machine's events outgrew the old fixed limit and held the daemon at two
// cores. Twice the live heap leaves a burst room to double between two
// tunings.
func memoryLimitFor(live uint64) int64 {
	return max(int64(memoryLimitFloor), int64(2*live))
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
