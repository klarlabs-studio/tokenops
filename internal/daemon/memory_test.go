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
