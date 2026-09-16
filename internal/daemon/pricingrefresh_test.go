package daemon

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
)

// Rate cards move on the order of weeks. A daily unconditional write
// would leave a year of near-identical snapshots and make `pricing diff`
// useless — so a check that found nothing still has to count as a check,
// or every restart re-fetches.
func TestLastCheckedMarkerMakesANoChangeCheckCount(t *testing.T) {
	dir := t.TempDir()
	if since := sinceLastCheck(dir); since < 24*time.Hour {
		t.Fatalf("an unchecked dir reported %v since last check; it must read as due", since)
	}
	markChecked(dir)
	if since := sinceLastCheck(dir); since > time.Minute {
		t.Errorf("after markChecked, sinceLastCheck = %v, want ~0", since)
	}
	if _, err := os.Stat(filepath.Join(dir, lastCheckedFile)); err != nil {
		t.Errorf("marker not written: %v", err)
	}
}

// A corrupt or truncated marker must read as "due", not as "just
// checked" — the failure direction that would silently stop refreshing.
func TestCorruptMarkerReadsAsDue(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, lastCheckedFile), []byte("not-a-time"), 0o600); err != nil {
		t.Fatal(err)
	}
	if since := sinceLastCheck(dir); since < 24*time.Hour {
		t.Errorf("a corrupt marker reported %v; it must read as due", since)
	}
}

// The interval has a floor and a default. Hammering a public file helps
// nobody, and a zero interval must not mean "constantly".
func TestRefreshIntervalDefaultsAndFloor(t *testing.T) {
	for _, tc := range []struct {
		in, want time.Duration
	}{
		{0, config.DefaultPricingRefresh},
		{time.Second, config.MinPricingRefresh},
		{48 * time.Hour, 48 * time.Hour},
	} {
		if got := (config.PricingRefreshConfig{Interval: tc.in}).Every(); got != tc.want {
			t.Errorf("Every(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// On by default, and switchable off — a tool that starts talking to the
// network without a way to stop it has spent trust it cannot buy back.
func TestRefreshIsOnByDefaultAndCanBeDisabled(t *testing.T) {
	if !(config.PricingRefreshConfig{}).Enabled() {
		t.Error("automatic refresh is off by default; a stale card prices new models at zero")
	}
	if (config.PricingRefreshConfig{Disabled: true}).Enabled() {
		t.Error("disabled: true did not turn it off")
	}
}

// A disabled refresh must not reach the network at all. The loop returns
// immediately rather than fetching once and then honouring the setting.
func TestDisabledRefreshReturnsWithoutFetching(t *testing.T) {
	cfg := config.Default()
	cfg.Pricing.Refresh.Disabled = true
	done := make(chan struct{})
	go func() {
		runPricingRefresh(t.Context(), cfg, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("runPricingRefresh did not return promptly when disabled")
	}
}
