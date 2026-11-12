package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// resolveStorageReadPath picks the sqlite path a read-only command opens.
// Order: --db flag, config.storage.path, ~/.tokenops/events.db.
func resolveStorageReadPath(flagPath, configPath string) (string, error) {
	if flagPath != "" {
		return flagPath, nil
	}
	if configPath != "" {
		return configPath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".tokenops", "events.db"), nil
}

// parseSince accepts either an RFC3339 timestamp or a Go duration like
// "24h" / "7d". Durations are subtracted from time.Now().
func parseSince(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// time.ParseDuration does not understand "d"; expand manually.
	if strings.HasSuffix(s, "d") {
		var days int
		if _, err := fmt.Sscanf(s, "%dd", &days); err == nil && days > 0 {
			return time.Now().Add(-time.Duration(days) * 24 * time.Hour), nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected RFC3339 or duration, got %q: %w", s, err)
	}
	return time.Now().Add(-d), nil
}

func fmtMoney(v float64, currency string) string {
	if currency == "" {
		currency = "USD"
	}
	return fmt.Sprintf("%.4f %s", v, currency)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}
