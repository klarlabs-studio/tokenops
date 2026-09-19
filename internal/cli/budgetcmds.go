package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// newBudgetCmd manages spend budgets, which only the MCP surface could set.
func newBudgetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "budget",
		Short: "List, set or remove spend budgets",
		Long: `A budget is a calendar window and a ceiling. In active mode the daemon
evaluates them and reports breaches.

On a flat-rate plan real spend is $0 at the margin, so a USD limit can
never trip: use --basis tokens with --limit-tokens, or --basis equivalent
to watch the API list-price value the subscription absorbed.`,
	}
	cmd.AddCommand(newBudgetListCmd(), newBudgetSetCmd(), newBudgetUnsetCmd())
	return cmd
}

func newBudgetListCmd() *cobra.Command {
	var configPathFlag string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured budgets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := resolveMutableConfigPath(configPathFlag)
			if err != nil {
				return err
			}
			cfg, err := readMutableConfig(path)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(cfg.Budgets) == 0 {
				fmt.Fprintln(out, "no budgets configured")
				return nil
			}
			for _, b := range cfg.Budgets {
				basis := b.Basis
				if basis == "" {
					basis = "spend"
				}
				limit := fmt.Sprintf("%.2f USD", b.LimitUSD)
				if strings.EqualFold(basis, "tokens") {
					limit = fmt.Sprintf("%d tokens", b.LimitTokens)
				}
				fmt.Fprintf(out, "  %-18s %-8s %-10s limit %s\n", b.Name, b.Window, basis, limit)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
	return cmd
}

func newBudgetSetCmd() *cobra.Command {
	var (
		configPathFlag string
		noRestartFlag  bool
		window         string
		limitUSD       float64
		limitTokens    int64
		warnAt, critAt float64
		basis          string
		workflowID     string
		agentID        string
	)
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Create or update a budget (upsert by name)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveMutableConfigPath(configPathFlag)
			if err != nil {
				return err
			}
			cfg, err := readMutableConfig(path)
			if err != nil {
				return err
			}
			if limitUSD <= 0 && limitTokens <= 0 {
				return fmt.Errorf("a budget needs a ceiling: pass --limit-usd, or --limit-tokens with --basis tokens")
			}
			b := config.BudgetConfig{
				Name:        args[0],
				Window:      window,
				LimitUSD:    limitUSD,
				LimitTokens: limitTokens,
				WarnAt:      warnAt,
				CritAt:      critAt,
				Basis:       basis,
				WorkflowID:  workflowID,
				AgentID:     agentID,
			}
			replaced := false
			for i, existing := range cfg.Budgets {
				if existing.Name == b.Name {
					cfg.Budgets[i] = b
					replaced = true
					break
				}
			}
			if !replaced {
				cfg.Budgets = append(cfg.Budgets, b)
			}
			if err := writeMutableConfig(path, cfg); err != nil {
				return err
			}
			verb := "added"
			if replaced {
				verb = "updated"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s budget %q (%s)\nwrote %s\n", verb, b.Name, b.Window, path)
			applyRestart(cmd.OutOrStdout(), !noRestartFlag, false)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
	cmd.Flags().StringVar(&window, "window", "monthly", "calendar window: daily | weekly | monthly")
	cmd.Flags().Float64Var(&limitUSD, "limit-usd", 0, "ceiling in USD")
	cmd.Flags().Int64Var(&limitTokens, "limit-tokens", 0, "ceiling in tokens (with --basis tokens)")
	cmd.Flags().Float64Var(&warnAt, "warn-at", 0, "fraction of the limit for a warning (default 0.75)")
	cmd.Flags().Float64Var(&critAt, "crit-at", 0, "fraction of the limit for a critical alert (default 0.95)")
	cmd.Flags().StringVar(&basis, "basis", "", "what the limit watches: spend | equivalent | tokens")
	cmd.Flags().StringVar(&workflowID, "workflow-id", "", "scope the budget to one workflow")
	cmd.Flags().StringVar(&agentID, "agent-id", "", "scope the budget to one agent")
	addNoRestartFlag(cmd, &noRestartFlag)
	return cmd
}

func newBudgetUnsetCmd() *cobra.Command {
	var (
		configPathFlag string
		noRestartFlag  bool
	)
	cmd := &cobra.Command{
		Use:   "unset <name>",
		Short: "Remove a budget by name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveMutableConfigPath(configPathFlag)
			if err != nil {
				return err
			}
			cfg, err := readMutableConfig(path)
			if err != nil {
				return err
			}
			kept := cfg.Budgets[:0]
			removed := false
			for _, b := range cfg.Budgets {
				if b.Name == args[0] {
					removed = true
					continue
				}
				kept = append(kept, b)
			}
			if !removed {
				fmt.Fprintf(cmd.OutOrStdout(), "no budget named %q; nothing to do\n", args[0])
				return nil
			}
			cfg.Budgets = kept
			if err := writeMutableConfig(path, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed budget %q\nwrote %s\n", args[0], path)
			applyRestart(cmd.OutOrStdout(), !noRestartFlag, false)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
	addNoRestartFlag(cmd, &noRestartFlag)
	return cmd
}

// newOptimizationsCmd lists optimization recommendations from the store.
func newOptimizationsCmd() *cobra.Command {
	var (
		dbPath     string
		since      string
		limit      int
		workflowID string
		agentID    string
	)
	cmd := &cobra.Command{
		Use:   "optimizations",
		Short: "List optimization recommendations recorded in the event store",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := resolveAuditDB(&rootFlags{}, dbPath)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := sqlite.Open(ctx, resolved, sqlite.Options{})
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			f := sqlite.Filter{
				Type:       eventschema.EventTypeOptimization,
				WorkflowID: workflowID,
				AgentID:    agentID,
				Limit:      limit,
			}
			if since != "" {
				t, perr := parseSince(since)
				if perr != nil {
					return perr
				}
				f.Since = t
			}
			events, err := store.Query(ctx, f)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(events) == 0 {
				fmt.Fprintln(out, "no optimizations recorded in that window")
				return nil
			}
			for _, e := range events {
				fmt.Fprintf(out, "  %s  %s\n", e.Timestamp.UTC().Format(time.RFC3339), e.Type)
			}
			fmt.Fprintf(out, "\n%d optimization event(s)\n", len(events))
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to config.storage.path)")
	cmd.Flags().StringVar(&since, "since", "7d", "lower bound (RFC3339 or duration)")
	cmd.Flags().IntVar(&limit, "limit", 50, "max rows")
	cmd.Flags().StringVar(&workflowID, "workflow-id", "", "filter to one workflow")
	cmd.Flags().StringVar(&agentID, "agent-id", "", "filter to one agent")
	return cmd
}
