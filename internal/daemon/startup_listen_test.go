package daemon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/infra/routehistory"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/internal/storage/sqlite/sqlitetest"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// healthBound is how soon after a start /healthz must answer. A restart
// waits 20s; the listener opening is the first thing the boot does after
// reading its config, so this leaves room only for a loaded CI runner.
const healthBound = 3 * time.Second

// syncBuffer is a log sink several goroutines write to.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startupStoreEvents sizes the synthetic store the start-up test boots
// on. TOKENOPS_STARTUP_TEST_EVENTS=320000 reproduces a store the size of
// the one that took 45s to answer.
func startupStoreEvents(t *testing.T) int {
	n := 20_000
	if v := os.Getenv("TOKENOPS_STARTUP_TEST_EVENTS"); v != "" {
		if _, err := fmt.Sscan(v, &n); err != nil {
			t.Fatalf("TOKENOPS_STARTUP_TEST_EVENTS: %v", err)
		}
	}
	return n
}

// TestDaemonAnswersHealthWhileWarmUpContinues pins the restart fix: on a
// large store, with the stored-id snapshot and the start-up corrections
// still running, /healthz and /version answer within healthBound and the
// daemon is ready; the warm-up finishes afterwards. The snapshot was read
// before the port opened, and on a real 317k-event store under load the
// listener answered only after 45s, past `daemon restart`'s 20s wait.
func TestDaemonAnswersHealthWhileWarmUpContinues(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "events.db")
	n := startupStoreEvents(t)
	store, err := sqlite.Open(context.Background(), dbPath, sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	sqlitetest.Fill(t, store, n)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	prevLoad, prevCorrect := loadStoredIDsFn, runStartupCorrections
	loadStoredIDsFn = func(ctx context.Context, s *sqlite.Store) (func(string) bool, int, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
		return prevLoad(ctx, s)
	}
	runStartupCorrections = func(ctx context.Context, _ config.Config, _ *sqlite.Store, _ *routehistory.Tracker, _ *slog.Logger) {
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	t.Cleanup(func() { loadStoredIDsFn, runStartupCorrections = prevLoad, prevCorrect })

	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	off := false
	cfg := config.Config{
		Listen:      net.JoinHostPort("127.0.0.1", port),
		Log:         config.LogConfig{Level: "info", Format: "text"},
		Storage:     config.StorageConfig{Enabled: true, Path: dbPath},
		VendorUsage: config.VendorUsageConfig{CodexAppServer: config.CodexAppServerConfig{Enabled: &off}},
		Shutdown:    config.ShutdownConfig{Timeout: 2 * time.Second},
	}
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	started := time.Now()
	go func() { errCh <- RunWithLogger(ctx, cfg, slog.New(slog.NewTextHandler(logs, nil))) }()
	stop := func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(10 * time.Second):
			t.Error("daemon did not shut down within 10s")
		}
	}

	base := "http://" + cfg.Listen
	if !waitForStatus(base+"/healthz", http.StatusOK, healthBound) {
		stop()
		t.Fatalf("/healthz did not answer within %s of the start (store of %d events, warm-up held)\n%s", healthBound, n, logs)
	}
	t.Logf("/healthz answered %s after the start, store of %d events", time.Since(started).Round(time.Millisecond), n)
	if code := getStatus(base + "/version"); code != http.StatusOK {
		t.Errorf("/version = %d, want 200", code)
	}
	if !waitForStatus(base+"/readyz", http.StatusOK, healthBound) {
		t.Errorf("/readyz not ready while the warm-up runs\n%s", logs)
	}
	if strings.Contains(logs.String(), "pollers skip stored events") {
		t.Errorf("the stored-id snapshot finished while it was held\n%s", logs)
	}

	unblock()
	deadline := time.Now().Add(30 * time.Second)
	want := fmt.Sprintf("stored_ids=%d", n)
	for !strings.Contains(logs.String(), want) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), want) {
		t.Errorf("warm-up never reported %s\n%s", want, logs)
	}
	stop()
}

// A poller that publishes before the snapshot is in waits for it, so
// history replayed straight after a start is still skipped; once the
// daemon stops, a waiting publish goes through rather than hanging.
func TestIngestionBusWaitsForTheStoredIDSnapshot(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	stored := &eventschema.Envelope{
		ID: "ccj-0011223344556677", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: time.Now(), Source: "test", Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic},
	}
	if err := store.Append(context.Background(), stored); err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	prev := loadStoredIDsFn
	loadStoredIDsFn = func(ctx context.Context, s *sqlite.Store) (func(string) bool, int, error) {
		<-release
		return prev(ctx, s)
	}
	t.Cleanup(func() { loadStoredIDsFn = prev })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.New(slog.DiscardHandler)
	sup := lifecycle.New(ctx, logger)
	rec := &idRecordingBus{}
	bus := ingestionBus(ctx, rec, store, sup, logger)

	published := make(chan struct{})
	go func() {
		bus.Publish(stored)
		bus.Publish(&eventschema.Envelope{ID: "ccj-new"})
		close(published)
	}()
	select {
	case <-published:
		t.Fatal("a publish went ahead of the stored-id snapshot")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-published:
	case <-time.After(5 * time.Second):
		t.Fatal("publishes still waiting after the snapshot was read")
	}
	if got := rec.ids(); len(got) != 1 || got[0] != "ccj-new" {
		t.Errorf("forwarded %v, want only ccj-new", got)
	}
	cancel()
	if err := sup.Wait(time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestIngestionBusPublishesThroughOnShutdown(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	block := make(chan struct{})
	prev := loadStoredIDsFn
	loadStoredIDsFn = func(context.Context, *sqlite.Store) (func(string) bool, int, error) {
		<-block
		return nil, 0, context.Canceled
	}
	t.Cleanup(func() { loadStoredIDsFn = prev })

	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.DiscardHandler)
	rec := &idRecordingBus{}
	sup := lifecycle.New(context.Background(), logger)
	defer func() {
		close(block)
		if err := sup.Wait(time.Second); err != nil {
			t.Error(err)
		}
	}()
	bus := ingestionBus(ctx, rec, store, sup, logger)
	cancel()
	done := make(chan struct{})
	go func() { bus.Publish(&eventschema.Envelope{ID: "ccj-late"}); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a publish hung on a snapshot that will never come")
	}
	if got := rec.ids(); len(got) != 1 {
		t.Errorf("forwarded %v, want the late envelope", got)
	}
}

// idRecordingBus records the ids published to it.
type idRecordingBus struct {
	events.Bus
	mu  sync.Mutex
	got []string
}

func (b *idRecordingBus) Publish(env *eventschema.Envelope) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.got = append(b.got, env.ID)
}

func (b *idRecordingBus) ids() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.got...)
}

func getStatus(url string) int {
	resp, err := http.Get(url)
	if err != nil {
		return 0
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func waitForStatus(url string, want int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if getStatus(url) == want {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
