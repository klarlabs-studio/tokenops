package cli

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/mcp"
)

const (
	daemonStartTimeout = 20 * time.Second
	daemonPollInterval = 200 * time.Millisecond
)

// awaitDaemonHealth is replaceable so lifecycle command tests do not bounce
// or probe the developer's real daemon.
var awaitDaemonHealth = waitForDaemonHealth

func confirmDaemonStarted(cmd *cobra.Command, rf *rootFlags) error {
	progress := startActivity(cmd.ErrOrStderr(), "Waiting for the daemon to become healthy")
	if err := waitForConfiguredDaemon(cmd.Context(), rf); err != nil {
		progress.failure("Daemon did not become healthy")
		return err
	}
	progress.success("Daemon is healthy")
	return nil
}

func waitForConfiguredDaemon(ctx context.Context, rf *rootFlags) error {
	cfg, err := loadConfig(rf)
	if err != nil {
		return fmt.Errorf("load daemon config while confirming startup: %w", err)
	}
	base := mcp.ConfiguredDaemonURL(cfg)
	if base == "" {
		return fmt.Errorf("daemon listen address %q cannot be probed", cfg.Listen)
	}
	if err := awaitDaemonHealth(ctx, base, daemonStartTimeout); err != nil {
		return fmt.Errorf("daemon did not answer %s/healthz within %s: %w; inspect `tokenops daemon status` and the daemon log", base, daemonStartTimeout, err)
	}
	return nil
}

func waitForDaemonHealth(ctx context.Context, base string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
		if err != nil {
			return err
		}
		resp, err := statusClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("health endpoint returned HTTP %d", resp.StatusCode)
		} else {
			lastErr = err
		}

		timer := time.NewTimer(daemonPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if lastErr != nil {
				return lastErr
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}
