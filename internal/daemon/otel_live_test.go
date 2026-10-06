package daemon

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/telemetry"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/otlp"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// TestOTelMetricsLive gathers this machine's figures as the daemon does
// and pushes them to the collector OTLP_LIVE_ENDPOINT names, reading the
// event store at OTLP_LIVE_STORE and the config at OTLP_LIVE_CONFIG (the
// package's tests run with HOME isolated).
func TestOTelMetricsLive(t *testing.T) {
	ep, storePath, path := os.Getenv("OTLP_LIVE_ENDPOINT"), os.Getenv("OTLP_LIVE_STORE"), os.Getenv("OTLP_LIVE_CONFIG")
	if ep == "" || storePath == "" || path == "" {
		t.Skip("set OTLP_LIVE_ENDPOINT, OTLP_LIVE_STORE and OTLP_LIVE_CONFIG")
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store, err := sqlite.OpenReadOnly(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	eng := spend.NewEngine(spend.DefaultTable())
	g := &telemetry.Gatherer{Glance: plansDeps(cfg, store, eng), Agg: analytics.New(store, eng)}
	now := time.Now().UTC()
	gauges := telemetry.Gauges(g.Gather(ctx, now))
	points := 0
	for _, gg := range gauges {
		points += len(gg.Points)
		t.Logf("%-36s %d points", gg.Name, len(gg.Points))
	}
	exp, err := otlp.NewMetrics(otlp.Options{Endpoint: ep, ServiceVersion: "live-test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err := exp.Push(ctx, gauges, now); err != nil {
		t.Fatal(err)
	}
	t.Logf("pushed %d gauges, %d points", len(gauges), points)
}
