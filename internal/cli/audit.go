package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/contexts/security/audit"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
)

func newAuditCmd(rf *rootFlags) *cobra.Command {
	var (
		dbPath  string
		action  string
		actor   string
		since   string
		until   string
		limit   int
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Query the local audit log",
		Long: `audit prints rows from the daemon's audit log (~/.tokenops/events.db
or --db). Filter by --action, --actor, --since (RFC3339 or duration like
24h / 7d), --until (RFC3339), and --limit.

Mirrors the tokenops_audit MCP tool: both call audit.Recorder.Query.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := resolveAuditDB(rf, dbPath)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			store, err := sqlite.Open(ctx, path, sqlite.Options{})
			if err != nil {
				return fmt.Errorf("open audit db: %w", err)
			}
			defer func() { _ = store.Close() }()
			rec := audit.NewRecorder(store)
			f := audit.Filter{Action: audit.Action(action), Actor: actor, Limit: limit}
			if since != "" {
				t, err := parseSince(since)
				if err != nil {
					return fmt.Errorf("--since: %w", err)
				}
				f.Since = t
			}
			if until != "" {
				t, err := time.Parse(time.RFC3339, until)
				if err != nil {
					return fmt.Errorf("--until: %w", err)
				}
				f.Until = t
			}
			entries, err := rec.Query(ctx, f)
			if err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
					Entries []audit.Entry `json:"entries"`
				}{Entries: entries})
			}
			renderAuditText(cmd, entries)
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to ~/.tokenops/events.db)")
	cmd.Flags().StringVar(&action, "action", "", "filter by audit action")
	cmd.Flags().StringVar(&actor, "actor", "", "filter by actor")
	cmd.Flags().StringVar(&since, "since", "", "lower bound (RFC3339 or duration like 24h, 7d)")
	cmd.Flags().StringVar(&until, "until", "", "upper bound (RFC3339)")
	cmd.Flags().IntVar(&limit, "limit", 100, "maximum entries returned")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	return cmd
}

func resolveAuditDB(rf *rootFlags, flagPath string) (string, error) {
	if flagPath != "" {
		return flagPath, nil
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

func renderAuditText(cmd *cobra.Command, entries []audit.Entry) {
	out := cmd.OutOrStdout()
	if len(entries) == 0 {
		fmt.Fprintln(out, "no audit entries")
		return
	}
	fmt.Fprintf(out, "%-25s %-22s %-15s %s\n", "WHEN", "ACTION", "ACTOR", "TARGET")
	for _, e := range entries {
		fmt.Fprintf(out, "%-25s %-22s %-15s %s\n",
			e.Timestamp.UTC().Format(time.RFC3339), string(e.Action), e.Actor, e.Target)
		if len(e.Details) > 0 {
			parts := make([]string, 0, len(e.Details))
			for k, v := range e.Details {
				parts = append(parts, fmt.Sprintf("%s=%v", k, v))
			}
			fmt.Fprintln(out, "  "+strings.Join(parts, " "))
		}
	}
}
