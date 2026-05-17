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

	"github.com/felixgeelhaar/tokenops/internal/contexts/tasks"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
)

// newTaskCmd assembles the `tokenops task` family. Three verbs
// today (start / done / list) — the cli surface stays flat so
// `tokenops task start "fix auth"` reads naturally.
func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Mark task boundaries so the scorecard + coach can compute task-level metrics",
		Long: `task records operator-marked task boundaries to
$HOME/.tokenops/tasks.jsonl. Once a task is started, scorecard and
coach commands can attribute spend, turns, and duration to that
task instead of to whatever 7-day window happens to be active.

Examples:
  tokenops task start "fix auth middleware"
  ...do the work...
  tokenops task done

  tokenops task list --since 30d`,
	}
	cmd.AddCommand(newTaskStartCmd())
	cmd.AddCommand(newTaskDoneCmd())
	cmd.AddCommand(newTaskListCmd())
	return cmd
}

func newTaskStartCmd() *cobra.Command {
	var (
		sessionID string
		pathFlag  string
	)
	cmd := &cobra.Command{
		Use:   "start <description>",
		Short: "Mark the start of a task",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			desc := strings.Join(args, " ")
			t, err := tasks.Start(pathFlag, desc, sessionID, nil)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"task started: %s\n  id: %s\n  started: %s\n  description: %s\n  run `tokenops task done` to close it\n",
				desc, t.ID, t.StartedAt.Format(time.RFC3339), desc,
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "optional Claude Code session ID to attribute the task to")
	cmd.Flags().StringVar(&pathFlag, "path", "", "tasks.jsonl path (defaults to ~/.tokenops/tasks.jsonl)")
	return cmd
}

func newTaskDoneCmd() *cobra.Command {
	var pathFlag string
	cmd := &cobra.Command{
		Use:   "done",
		Short: "Mark the most recent open task as complete",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := tasks.Done(pathFlag, nil)
			if err != nil {
				return err
			}
			dur := t.Duration(time.Now)
			fmt.Fprintf(cmd.OutOrStdout(),
				"task done: %s\n  id: %s\n  duration: %s\n  completed: %s\n",
				t.Description, t.ID, dur.Round(time.Second), t.CompletedAt.Format(time.RFC3339),
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&pathFlag, "path", "", "tasks.jsonl path (defaults to ~/.tokenops/tasks.jsonl)")
	return cmd
}

func newTaskListCmd() *cobra.Command {
	var (
		jsonOut     bool
		pathFlag    string
		since       string
		dbPath      string
		withMetrics bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recorded tasks (chronological)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			all, err := tasks.List(pathFlag)
			if err != nil {
				return err
			}
			if since != "" {
				cutoff, err := parseSince(since)
				if err != nil {
					return fmt.Errorf("--since: %w", err)
				}
				filtered := all[:0]
				for _, t := range all {
					if !t.StartedAt.Before(cutoff) {
						filtered = append(filtered, t)
					}
				}
				all = filtered
			}
			var metrics map[string]tasks.Metrics
			if withMetrics {
				m, err := computeTaskMetrics(cmd.Context(), all, dbPath)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: metrics unavailable: %v\n", err)
				} else {
					metrics = m
				}
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if metrics != nil {
					return enc.Encode(map[string]any{"tasks": all, "metrics": metrics})
				}
				return enc.Encode(all)
			}
			renderTaskList(cmd, all, metrics)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	cmd.Flags().StringVar(&pathFlag, "path", "", "tasks.jsonl path (defaults to ~/.tokenops/tasks.jsonl)")
	cmd.Flags().StringVar(&since, "since", "", "filter tasks started at or after this point (RFC3339 or duration like 7d)")
	cmd.Flags().BoolVar(&withMetrics, "metrics", false, "enrich each task with cost / turns / TTFUO from the local event store")
	cmd.Flags().StringVar(&dbPath, "db", "", "events.db path for --metrics (defaults to ~/.tokenops/events.db)")
	return cmd
}

// computeTaskMetrics opens the local sqlite events.db once and rolls
// up per-task metrics in a single pass. The store is opened
// read-only for the duration of the command; closed before return.
func computeTaskMetrics(ctx context.Context, all []tasks.Task, dbPath string) (map[string]tasks.Metrics, error) {
	if dbPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dbPath = filepath.Join(home, ".tokenops", "events.db")
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("events.db not found at %s; run `tokenops init` first", dbPath)
	}
	store, err := sqlite.Open(ctx, dbPath, sqlite.Options{})
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = store.Close() }()
	out := make(map[string]tasks.Metrics, len(all))
	for _, t := range all {
		m, err := tasks.MetricsFor(ctx, store, t, nil)
		if err != nil {
			return nil, fmt.Errorf("metrics for %s: %w", t.ID, err)
		}
		out[t.ID] = m
	}
	return out, nil
}

func renderTaskList(cmd *cobra.Command, all []tasks.Task, metrics map[string]tasks.Metrics) {
	out := cmd.OutOrStdout()
	if len(all) == 0 {
		fmt.Fprintln(out, "No tasks recorded. Run `tokenops task start <description>` to begin.")
		return
	}
	fmt.Fprintln(out, "Tasks")
	if metrics == nil {
		fmt.Fprintf(out, "  %-19s %-10s %-44s %s\n", "STARTED", "DURATION", "DESCRIPTION", "STATUS")
	} else {
		fmt.Fprintf(out, "  %-19s %-10s %-32s %-7s %6s %8s %8s %s\n",
			"STARTED", "DURATION", "DESCRIPTION", "STATUS", "TURNS", "TTFUO", "$/TURN", "COST")
	}
	for _, t := range all {
		status := "OPEN"
		if !t.CompletedAt.IsZero() {
			status = "done"
		}
		dur := t.Duration(time.Now).Round(time.Second)
		desc := t.Description
		if metrics == nil {
			if len(desc) > 44 {
				desc = desc[:42] + "…"
			}
			fmt.Fprintf(out, "  %-19s %-10s %-44s %s\n",
				t.StartedAt.Format("2006-01-02 15:04:05"),
				dur, desc, status,
			)
			continue
		}
		if len(desc) > 32 {
			desc = desc[:30] + "…"
		}
		m := metrics[t.ID]
		ttfuo := "—"
		if m.TTFUOSeconds > 0 {
			ttfuo = fmt.Sprintf("%.1fs", m.TTFUOSeconds)
		}
		fmt.Fprintf(out, "  %-19s %-10s %-32s %-7s %6d %8s %8.4f %7.2f\n",
			t.StartedAt.Format("2006-01-02 15:04:05"),
			dur, desc, status,
			m.Turns, ttfuo, m.CostPerTurn, m.CostUSD,
		)
	}
}
