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

	oldPct := debug.SetGCPercent(100)
	defer debug.SetGCPercent(oldPct)

	t.Setenv(memoryLimitEnv, "")
	t.Setenv(gcPercentEnv, "")
	if err := s.limitMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := debug.SetMemoryLimit(-1); got != memoryLimitFloor {
		t.Errorf("limit = %d, want %d", got, memoryLimitFloor)
	}
	if got := debug.SetGCPercent(100); got != gcPercent {
		t.Errorf("GC percent = %d, want %d", got, gcPercent)
	}
	t.Setenv(gcPercentEnv, "200")
	debug.SetGCPercent(200)
	if err := s.limitMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := debug.SetGCPercent(200); got != 200 {
		t.Errorf("GOGC was overridden: %d", got)
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

// The heap grows half again the live heap before a collection (GOGC 50),
// not twice it: with a month of events packed the live heap is a few
// hundred MB, and doubling it was most of the daemon's footprint. The soft
// limit stays twice the live heap, well above that target, so it never
// binds in steady work; it never sits below the live heap either, where
// the collector runs back to back (Go caps it at half the CPU), which on
// a busy store held the daemon at two cores.
func TestMemoryLimitFollowsTheLiveHeap(t *testing.T) {
	for _, tc := range []struct {
		name string
		live uint64
		want int64
	}{
		{"small heap keeps the floor", 100 << 20, memoryLimitFloor},
		{"the packed month's heap", 320 << 20, 640 << 20},
		{"large heap", 1300 << 20, 2600 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := memoryLimitFor(tc.live); got != tc.want {
				t.Errorf("memoryLimitFor(%d) = %d, want %d", tc.live, got, tc.want)
			}
		})
	}
	if memoryLimitFloor > 300<<20 {
		t.Errorf("the floor %d MiB is above the live heap the daemon holds on a busy store; it would let the heap double again", memoryLimitFloor>>20)
	}
	for live := uint64(1 << 20); live < 8<<30; live += 37 << 20 {
		if target := live + live*gcPercent/100; uint64(memoryLimitFor(live)) < target+target/4 {
			t.Fatalf("live %d MiB: limit %d MiB is within a quarter of the collector's target %d MiB", live>>20, memoryLimitFor(live)>>20, target>>20)
		}
	}
}

// The limit moves with the heap, both ways, and only on a change worth
// making.
func TestApplyMemoryLimitMovesOnlyOnARealChange(t *testing.T) {
	old := debug.SetMemoryLimit(-1)
	defer debug.SetMemoryLimit(old)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	debug.SetMemoryLimit(memoryLimitFloor)
	if got := applyMemoryLimit(1300<<20, logger); got != 2600<<20 || debug.SetMemoryLimit(-1) != 2600<<20 {
		t.Fatalf("live 1300 MiB: limit %d, want 2600 MiB", got)
	}
	// A few percent more live heap is not worth a change.
	if got := applyMemoryLimit(1340<<20, logger); got != 2600<<20 {
		t.Errorf("small growth moved the limit to %d", got)
	}
	// The heap shrank: back to the floor.
	if got := applyMemoryLimit(100<<20, logger); got != memoryLimitFloor {
		t.Errorf("shrunk heap left the limit at %d", got)
	}
}
