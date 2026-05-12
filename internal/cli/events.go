package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/domainevents"
)

// resolveDomainLogPath returns the JSONL location, honoring
// cfg.Storage.Path so the log shares its directory with events.db
// instead of hardcoding ~/.tokenops.
func resolveDomainLogPath(rf *rootFlags) (string, error) {
	if rf != nil {
		if cfg, err := loadConfig(rf); err == nil && cfg.Storage.Path != "" {
			return filepath.Join(filepath.Dir(cfg.Storage.Path), "domain-events.jsonl"), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".tokenops", "domain-events.jsonl"), nil
}

func newEventsCmd(rf *rootFlags) *cobra.Command {
	var (
		addr    string
		jsonOut bool
		since   string
		until   string
	)
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Show per-kind domain event counts",
		Long: `events queries the daemon's /api/domain-events endpoint and
prints in-process domain event counts (workflow.started,
optimization.applied, rule_corpus.reloaded, budget.exceeded, ...).
Mirrors the tokenops_domain_events MCP tool.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var sinceT, untilT time.Time
			if since != "" {
				t, err := parseSince(since)
				if err != nil {
					return fmt.Errorf("--since: %w", err)
				}
				sinceT = t
			}
			if until != "" {
				t, err := time.Parse(time.RFC3339, until)
				if err != nil {
					return fmt.Errorf("--until: %w", err)
				}
				untilT = t
			}
			if !sinceT.IsZero() || !untilT.IsZero() {
				// Time bounds → JSONL fallback; HTTP returns lifetime totals.
				return renderFromLogWindow(cmd, rf, jsonOut, sinceT, untilT)
			}
			target := addr
			if target == "" {
				cfg, err := loadConfig(rf)
				if err != nil {
					return err
				}
				target = cfg.Listen
			}
			url := target + "/api/domain-events"
			if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
				url = "http://" + target + "/api/domain-events"
			}
			client := &http.Client{Timeout: 3 * time.Second}
			resp, err := client.Get(url)
			if err != nil {
				return renderFromLogSince(cmd, rf, jsonOut, time.Time{})
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode == http.StatusNotFound {
				return renderFromLogSince(cmd, rf, jsonOut, time.Time{})
			}
			if resp.StatusCode >= 300 {
				return fmt.Errorf("daemon returned %d: %s", resp.StatusCode, body)
			}
			if jsonOut {
				cmd.Println(string(body))
				return nil
			}
			var payload struct {
				Counts map[string]int64 `json:"counts"`
				Total  int64            `json:"total"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "total: %d\n\n", payload.Total)
			fmt.Fprintf(cmd.OutOrStdout(), "%-32s %10s\n", "KIND", "COUNT")
			for k, v := range payload.Counts {
				fmt.Fprintf(cmd.OutOrStdout(), "%-32s %10d\n", k, v)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "", "daemon host:port (defaults to config.listen)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	cmd.Flags().StringVar(&since, "since", "", "JSONL fallback only: tally events ≥ this time (RFC3339 or duration like 24h)")
	cmd.Flags().StringVar(&until, "until", "", "JSONL fallback only: upper bound (RFC3339)")
	return cmd
}

// renderFromLog falls back to scanning the persisted domain-events
// JSONL log (~/.tokenops/domain-events.jsonl) when the daemon's HTTP
// surface is unreachable. Tallies per-kind counts the same way the
// in-process counter does.
func renderFromLogSince(cmd *cobra.Command, rf *rootFlags, jsonOut bool, since time.Time) error {
	return renderFromLogWindow(cmd, rf, jsonOut, since, time.Time{})
}

func renderFromLogWindow(cmd *cobra.Command, rf *rootFlags, jsonOut bool, since, until time.Time) error {
	path, err := resolveDomainLogPath(rf)
	if err != nil {
		return err
	}
	counts := map[string]int64{}
	var total int64
	if err := domainevents.Replay(path, func(r domainevents.Record) error {
		if !since.IsZero() && r.At.Before(since) {
			return nil
		}
		if !until.IsZero() && r.At.After(until) {
			return nil
		}
		counts[r.Kind]++
		total++
		return nil
	}); err != nil {
		return fmt.Errorf("replay log %s: %w", path, err)
	}
	if jsonOut {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
			"counts": counts,
			"total":  total,
			"source": "jsonl",
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "source: jsonl (%s)\ntotal: %d\n\n", path, total)
	fmt.Fprintf(cmd.OutOrStdout(), "%-32s %10s\n", "KIND", "COUNT")
	for k, v := range counts {
		fmt.Fprintf(cmd.OutOrStdout(), "%-32s %10d\n", k, v)
	}
	return nil
}
