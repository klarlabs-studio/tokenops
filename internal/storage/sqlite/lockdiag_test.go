package sqlite

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Every tokenops process shares one store, and the write lock is sometimes
// held for seconds by a process nobody has named. A slow wait for, or hold
// of, the write lock is logged with the process that saw it.
func TestAppendBatchLogsSlowLockWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	ctx := context.Background()
	var buf bytes.Buffer
	s, err := Open(ctx, path, Options{
		BusyTimeout:  2 * time.Second,
		Logger:       slog.New(slog.NewTextHandler(&buf, nil)),
		SlowLockWarn: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	holder, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	conn, err := holder.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("holder conn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("take write lock: %v", err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
	}()

	env := mustPromptEnvelope(t, "slow-1", time.Now(), &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic})
	if err := s.AppendBatch(ctx, []*eventschema.Envelope{env}); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "sqlite: slow write lock") || !strings.Contains(out, "pid=") || !strings.Contains(out, "wait=") {
		t.Fatalf("no slow-lock diagnostic logged: %q", out)
	}
}

func TestAppendBatchQuietWhenFast(t *testing.T) {
	var buf bytes.Buffer
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "events.db"), Options{
		Logger: slog.New(slog.NewTextHandler(&buf, nil)),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	env := mustPromptEnvelope(t, "fast-1", time.Now(), &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic})
	if err := s.AppendBatch(context.Background(), []*eventschema.Envelope{env}); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}
	if strings.Contains(buf.String(), "slow write lock") {
		t.Fatalf("fast append logged a slow-lock diagnostic: %q", buf.String())
	}
}
