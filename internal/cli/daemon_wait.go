package cli

import (
	"context"
	"encoding/json"
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

// warmingUpNote is what a lifecycle command says when the daemon answers
// its health check but is still loading what it serves.
const warmingUpNote = "it is still warming up: the API answers once it has opened its store, usually within seconds"

func confirmDaemonStarted(cmd *cobra.Command, rf *rootFlags) error {
	progress := startActivity(cmd.ErrOrStderr(), "Waiting for the daemon to become healthy")
	warming, err := waitForConfiguredDaemon(cmd.Context(), rf)
	if err != nil {
		progress.failure("Daemon did not become healthy")
		return err
	}
	if warming {
		progress.success("Daemon started; warming up")
		fmt.Fprintln(cmd.OutOrStdout(), warmingUpNote)
		return nil
	}
	progress.success("Daemon is healthy")
	return nil
}

// waitForConfiguredDaemon waits for the configured daemon's /healthz.
// warming is true when it answered while still starting.
func waitForConfiguredDaemon(ctx context.Context, rf *rootFlags) (warming bool, err error) {
	cfg, err := loadConfig(rf)
	if err != nil {
		return false, fmt.Errorf("load daemon config while confirming startup: %w", err)
	}
	base := mcp.ConfiguredDaemonURL(cfg)
	if base == "" {
		return false, fmt.Errorf("daemon listen address %q cannot be probed", cfg.Listen)
	}
	warming, err = awaitDaemonHealth(ctx, base, daemonStartTimeout)
	if err != nil {
		return false, fmt.Errorf("daemon did not answer %s/healthz within %s: %w; inspect `tokenops daemon status` and the daemon log", base, daemonStartTimeout, err)
	}
	return warming, nil
}

// waitForDaemonHealth polls base/healthz until it answers 200 or timeout
// passes. A daemon answers from the first moments of its boot, with
// "state": "starting" until it serves everything; warming reports that,
// so a restart is not called a failure because the store is large.
func waitForDaemonHealth(ctx context.Context, base string, timeout time.Duration) (warming bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
		if err != nil {
			return false, err
		}
		resp, err := statusClient.Do(req)
		if err == nil {
			var body struct {
				State string `json:"state"`
			}
			if resp.StatusCode == http.StatusOK {
				_ = json.NewDecoder(resp.Body).Decode(&body)
			}
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return body.State == "starting", nil
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
				return false, lastErr
			}
			return false, ctx.Err()
		case <-timer.C:
		}
	}
}
