package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func newEventsCmd(rf *rootFlags) *cobra.Command {
	var (
		dbPath  string
		jsonOut bool
		since   string
		until   string
	)
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Show per-kind domain event counts",
		Long: `events queries the canonical local event store and prints per-kind
domain event counts (workflow.started, optimization.applied,
rule_corpus.reloaded, budget.exceeded, ...). Mirrors the
tokenops_domain_events MCP tool.`,
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
				// SQLite's upper bound is exclusive; preserve the CLI's
				// historical inclusive --until behavior at nanosecond precision.
				untilT = t.Add(time.Nanosecond)
			}
			path, err := resolveEventsDB(rf, dbPath)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			store, err := sqlite.Open(ctx, path, sqlite.Options{})
			if err != nil {
				return fmt.Errorf("open event store: %w", err)
			}
			defer func() { _ = store.Close() }()
			events, err := store.Query(ctx, sqlite.Filter{
				Type: eventschema.EventTypeDomain, Since: sinceT, Until: untilT, Limit: 1_000_000,
			})
			if err != nil {
				return fmt.Errorf("query domain events: %w", err)
			}
			counts := make(map[string]int64)
			var total int64
			for _, env := range events {
				if env == nil {
					continue
				}
				payload, ok := env.Payload.(*eventschema.DomainEvent)
				if !ok || payload.Kind == "" {
					continue
				}
				counts[payload.Kind]++
				total++
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
					"counts": counts, "total": total, "source": "sqlite",
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "source: sqlite (%s)\ntotal: %d\n\n", path, total)
			fmt.Fprintf(cmd.OutOrStdout(), "%-32s %10s\n", "KIND", "COUNT")
			kinds := make([]string, 0, len(counts))
			for kind := range counts {
				kinds = append(kinds, kind)
			}
			sort.Strings(kinds)
			for _, kind := range kinds {
				fmt.Fprintf(cmd.OutOrStdout(), "%-32s %10d\n", kind, counts[kind])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to configured storage path or ~/.tokenops/events.db)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	cmd.Flags().StringVar(&since, "since", "", "lower bound (RFC3339 or duration like 24h)")
	cmd.Flags().StringVar(&until, "until", "", "inclusive upper bound (RFC3339)")
	return cmd
}

func resolveEventsDB(rf *rootFlags, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if rf != nil {
		if cfg, err := loadConfig(rf); err == nil && cfg.Storage.Path != "" {
			return cfg.Storage.Path, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".tokenops", "events.db"), nil
}
