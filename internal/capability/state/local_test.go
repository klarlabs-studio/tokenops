package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
)

func TestOfDaemon(t *testing.T) {
	cases := []struct {
		readyz   string
		warnings bool
		want     string
	}{
		{"ready", false, Ready},
		{"ready", true, Degraded},
		{"not_configured", false, NotConfigured},
		{"not_configured", true, NotConfigured},
		{"not_ready", false, NotReady},
		// A daemon that did not say is not assumed ready.
		{"", false, NotReady},
		{"something-new", false, NotReady},
	}
	for _, tc := range cases {
		if got := OfDaemon(tc.readyz, tc.warnings); got != tc.want {
			t.Errorf("OfDaemon(%q, %v) = %q, want %q", tc.readyz, tc.warnings, got, tc.want)
		}
	}
}

// counterFunc adapts a function to config.SourceCounter.
type counterFunc func() (map[string]int64, error)

func (f counterFunc) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return f()
}

func TestLocalWarningsNamesAnUnmatchedRetentionRule(t *testing.T) {
	cfg := config.Config{Retention: config.RetentionConfig{KeepBySource: map[string]string{"no-such-source": "30d"}}}
	got := LocalWarnings(context.Background(), cfg, counterFunc(func() (map[string]int64, error) {
		return map[string]int64{"proxy": 3}, nil
	}), time.Now())
	if len(got) != 1 || !strings.Contains(got[0], `retention.keep_by_source["no-such-source"]`) {
		t.Fatalf("warnings = %q", got)
	}
}

// A store that cannot be counted says nothing rather than failing status.
func TestLocalWarningsIsSilentOnAStoreError(t *testing.T) {
	cfg := config.Config{Retention: config.RetentionConfig{KeepBySource: map[string]string{"no-such-source": "30d"}}}
	got := LocalWarnings(context.Background(), cfg, counterFunc(func() (map[string]int64, error) {
		return nil, errors.New("locked")
	}), time.Now())
	if got != nil {
		t.Fatalf("warnings = %q, want none", got)
	}
}
