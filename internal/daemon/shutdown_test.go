package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"
)

type fakeHTTPServer struct {
	shutdownErr error
	calls       *[]string
}

func (f fakeHTTPServer) Shutdown(context.Context) error {
	*f.calls = append(*f.calls, "shutdown")
	return f.shutdownErr
}

func (f fakeHTTPServer) Close() error {
	*f.calls = append(*f.calls, "close")
	return nil
}

func TestStopDaemonDrainsEvenWhenHTTPShutdownTimesOut(t *testing.T) {
	for _, tc := range []struct {
		name        string
		shutdownErr error
		wantCalls   []string
		wantErr     bool
	}{
		{
			name:      "clean shutdown",
			wantCalls: []string{"shutdown", "wait", "drain", "components"},
		},
		{
			name:        "request outlives the grace period",
			shutdownErr: context.DeadlineExceeded,
			wantCalls:   []string{"shutdown", "close", "wait", "drain", "components"},
			wantErr:     true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			record := func(name string) func(time.Duration) error {
				return func(time.Duration) error { calls = append(calls, name); return nil }
			}
			err := stopDaemon(slog.New(slog.NewTextHandler(io.Discard, nil)), time.Millisecond, shutdownSteps{
				server:          fakeHTTPServer{shutdownErr: tc.shutdownErr, calls: &calls},
				waitSubsystems:  record("wait"),
				drainEvents:     record("drain"),
				closeComponents: func() { calls = append(calls, "components") },
				running:         func() []string { return nil },
			})
			if !slices.Equal(calls, tc.wantCalls) {
				t.Fatalf("stages = %v, want %v", calls, tc.wantCalls)
			}
			if gotErr := err != nil; gotErr != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, tc.shutdownErr) {
				t.Fatalf("err = %v, want it to wrap %v", err, tc.shutdownErr)
			}
		})
	}
}
