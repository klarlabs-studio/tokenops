package daemon

import (
	"context"
	"io"
	"log/slog"
	"runtime/debug"
	"testing"
)

func TestLimitMemorySetsADefaultAndKeepsGOMEMLIMIT(t *testing.T) {
	old := debug.SetMemoryLimit(-1)
	defer debug.SetMemoryLimit(old)
	s := &startup{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	t.Setenv(memoryLimitEnv, "")
	if err := s.limitMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := debug.SetMemoryLimit(-1); got != defaultMemoryLimit {
		t.Errorf("limit = %d, want %d", got, defaultMemoryLimit)
	}
	debug.SetMemoryLimit(123 << 20)
	t.Setenv(memoryLimitEnv, "123MiB")
	if err := s.limitMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := debug.SetMemoryLimit(-1); got != 123<<20 {
		t.Errorf("GOMEMLIMIT was overridden: %d", got)
	}
}

func TestProfilerIsOptInAndLoopbackOnly(t *testing.T) {
	s := &startup{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	defer s.runCleanups()
	t.Setenv(profilerEnv, "")
	if err := s.startProfiler(context.Background()); err != nil || len(s.cleanups) != 0 {
		t.Fatalf("profiler started without being asked: %v", err)
	}
	for _, bad := range []string{"0.0.0.0:0", ":0", "192.168.1.5:6060", "example.com:6060"} {
		t.Setenv(profilerEnv, bad)
		if err := s.startProfiler(context.Background()); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	for _, ok := range []string{"127.0.0.1:6060", "[::1]:6060", "localhost:6060"} {
		if err := loopbackOnly(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	t.Setenv(profilerEnv, "127.0.0.1:0")
	if err := s.startProfiler(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.cleanups) != 1 {
		t.Fatal("no cleanup registered")
	}
}

// The soft limit never sits below the live heap: a limit the working set
// has outgrown makes the collector run back to back (Go caps it at half
// the CPU), which on a busy store held the daemon at two cores.
func TestMemoryLimitFollowsTheLiveHeap(t *testing.T) {
	for _, tc := range []struct {
		name string
		live uint64
		want int64
	}{
		{"small heap keeps the default", 200 << 20, defaultMemoryLimit},
		{"heap at two thirds of the default keeps it", 682 << 20, defaultMemoryLimit},
		{"heap past two thirds gets half again of headroom", 1 << 30, 3 << 29},
		{"large heap", 2 << 30, 3 << 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := memoryLimitFor(tc.live); got != tc.want {
				t.Errorf("memoryLimitFor(%d) = %d, want %d", tc.live, got, tc.want)
			}
		})
	}
}

// The limit moves with the heap, both ways, and only on a change worth
// making.
func TestApplyMemoryLimitMovesOnlyOnARealChange(t *testing.T) {
	old := debug.SetMemoryLimit(-1)
	defer debug.SetMemoryLimit(old)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	debug.SetMemoryLimit(defaultMemoryLimit)
	if got := applyMemoryLimit(1300<<20, logger); got != 1950<<20 || debug.SetMemoryLimit(-1) != 1950<<20 {
		t.Fatalf("live 1300 MiB: limit %d, want 1950 MiB", got)
	}
	// A few percent more live heap is not worth a change.
	if got := applyMemoryLimit(1340<<20, logger); got != 1950<<20 {
		t.Errorf("small growth moved the limit to %d", got)
	}
	// The heap shrank: back to the default.
	if got := applyMemoryLimit(300<<20, logger); got != defaultMemoryLimit {
		t.Errorf("shrunk heap left the limit at %d", got)
	}
}
