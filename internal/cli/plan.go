package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func newPlanCmd(rf *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "List subscription plans + compute headroom",
		Long: `plan inspects the flat-rate subscription configuration (Claude Max,
ChatGPT Plus, GitHub Copilot, Cursor, etc.) and reports remaining
quota / overage risk based on plan_included events in the local
store. Subcommands:

  tokenops plan list       — show the configured plans
  tokenops plan headroom   — compute current consumption + risk
  tokenops plan catalog    — list every plan TokenOps knows about`,
	}
	cmd.AddCommand(
		newPlanListCmd(rf),
		newPlanHeadroomCmd(rf),
		newPlanCatalogCmd(),
		newPlanSetCmd(),
		newPlanUnsetCmd(),
	)
	return cmd
}

func newPlanSetCmd() *cobra.Command {
	var (
		configPathFlag string
		noRestartFlag  bool
		spendLimit     float64
		limitWindow    string
		rateFactor     float64
	)
	cmd := &cobra.Command{
		Use:   "set <provider> <plan>",
		Short: "Bind a provider to a subscription plan in config.yaml",
		Long: `set writes plans.<provider> = <plan> to the active config file so the
daemon and MCP server pick up the binding on next start. Replaces the
previous workflow of editing the MCP host's JSON env block.

Example:
  tokenops plan set anthropic claude-max-20x
  tokenops plan set openai gpt-plus`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider, planName := args[0], args[1]
			path, err := resolveMutableConfigPath(configPathFlag)
			if err != nil {
				return err
			}
			cfg, err := readMutableConfig(path)
			if err != nil {
				return err
			}
			b, err := cfg.BindPlan(provider, planName, config.PlanLimit{
				SpendLimitUSD: spendLimit, Window: limitWindow, RateFactor: rateFactor,
			})
			if err != nil {
				return err
			}
			if b.RenamedFrom != "" {
				fmt.Fprintf(cmd.OutOrStdout(),
					"renamed %s -> %s (catalog migrated in v0.6.0; using the modern name)\n",
					b.RenamedFrom, b.Plan,
				)
			}
			if err := writeMutableConfig(path, cfg); err != nil {
				return err
			}
			if b.Previous != "" && b.Previous != b.Plan {
				fmt.Fprintf(cmd.OutOrStdout(), "updated plans.%s: %s -> %s\n", provider, b.Previous, b.Plan)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "set plans.%s = %s\n", provider, b.Plan)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
			applyRestart(cmd.OutOrStdout(), !noRestartFlag, true)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
	cmd.Flags().Float64Var(&spendLimit, "spend-limit", 0,
		"org spend limit in USD, for plans billed at API rates rather than rate-limited (Enterprise)")
	cmd.Flags().StringVar(&limitWindow, "limit-window", "",
		"period the spend limit covers: monthly (default), weekly or daily")
	cmd.Flags().Float64Var(&rateFactor, "rate-factor", 0,
		"scale measured spend to a negotiated rate (0.8 = 20% off list); default is list price")
	addNoRestartFlag(cmd, &noRestartFlag)
	return cmd
}

func newPlanUnsetCmd() *cobra.Command {
	var (
		configPathFlag string
		noRestartFlag  bool
	)
	cmd := &cobra.Command{
		Use:   "unset <provider>",
		Short: "Remove a provider's plan binding from config.yaml",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := args[0]
			path, err := resolveMutableConfigPath(configPathFlag)
			if err != nil {
				return err
			}
			cfg, err := readMutableConfig(path)
			if err != nil {
				return err
			}
			if _, ok := cfg.Plans[provider]; !ok {
				fmt.Fprintf(cmd.OutOrStdout(), "plans.%s not set; nothing to do\n", provider)
				return nil
			}
			delete(cfg.Plans, provider)
			if err := writeMutableConfig(path, cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"removed plans.%s\nwrote %s\n",
				provider, path,
			)
			applyRestart(cmd.OutOrStdout(), !noRestartFlag, true)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
	addNoRestartFlag(cmd, &noRestartFlag)
	return cmd
}

func resolveMutableConfigPath(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return defaultConfigPath()
}

func newPlanListCmd(rf *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured plans (provider → plan name)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			if len(cfg.Plans) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no plans configured; run `tokenops plan set <provider> <plan>` (e.g. `tokenops plan set anthropic claude-max-20x`)")
				return nil
			}
			// Sorted for the same reason headroom is: a table whose rows
			// reshuffle between runs is one an operator cannot diff.
			for _, provider := range sortedPlanProviders(cfg.Plans) {
				planName := cfg.Plans[provider]
				p, ok := plans.Lookup(planName)
				if !ok {
					fmt.Fprintf(cmd.OutOrStdout(), "%-12s %s (unknown plan!)\n", provider, planName)
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-12s %s (%s)\n", provider, p.Display, planName)
			}
			return nil
		},
	}
}

func newPlanHeadroomCmd(rf *rootFlags) *cobra.Command {
	var dbPath string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "headroom",
		Short: "Compute month-to-date headroom for every configured plan",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			if len(cfg.Plans) == 0 {
				return errors.New(headroom.UnconfiguredHint)
			}
			resolvedPath, err := resolvePlanDB(dbPath)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			store, err := sqlite.Open(ctx, resolvedPath, sqlite.Options{})
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer func() { _ = store.Close() }()

			// The capability decides which plans, in what order, with
			// which limits. This command decides how to print them, which
			// is the only part that legitimately differs from the MCP
			// tool answering the same question.
			res, err := headroom.Compute(ctx, headroom.Deps{
				Config: &cfg,
				Reader: storeReader{store: store},
			}, time.Now().UTC())
			if err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(res.Reports)
			}
			for _, note := range res.Notes {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", note)
			}
			for _, r := range res.Reports {
				fmt.Fprintf(cmd.OutOrStdout(),
					"%s (%s) — risk %s\n",
					r.Display, r.PlanName, r.OverageRisk,
				)
				if r.QuotaTokens > 0 {
					fmt.Fprintf(cmd.OutOrStdout(),
						"  monthly: %d / %d tokens (%.1f%%)",
						r.ConsumedTokens, r.QuotaTokens, r.ConsumedPct,
					)
					if !math.IsNaN(r.HeadroomDays) && r.HeadroomDays > 0 {
						fmt.Fprintf(cmd.OutOrStdout(), " — %.1f days headroom", r.HeadroomDays)
					}
					fmt.Fprintln(cmd.OutOrStdout())
				} else if r.SpendLimitUSD == 0 && r.SpendUSD == 0 {
					fmt.Fprintf(cmd.OutOrStdout(),
						"  tokens this month: %d (no monthly cap)\n", r.ConsumedTokens,
					)
				}
				if r.WindowCap > 0 {
					fmt.Fprintf(cmd.OutOrStdout(),
						"  window:  %d / %d %s per %s (%.1f%%) — resets in %s\n",
						r.WindowConsumed, r.WindowCap, r.WindowUnit,
						r.WindowDuration, r.WindowPct, r.WindowResetsIn,
					)
				}
				// A spend-denominated plan reports money against the org's
				// own limit; it has no window and renders none.
				if r.SpendLimitUSD > 0 {
					fmt.Fprintf(cmd.OutOrStdout(),
						"  spend:   %.2f / %.2f USD (%.1f%%) — %s\n",
						r.SpendUSD, r.SpendLimitUSD, r.SpendPct, spendSourceLabel(r.SpendSource),
					)
				} else if r.SpendUSD > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "  spend:   %.2f USD (no limit configured)\n", r.SpendUSD)
				}
				if r.Note != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  note: %s\n", r.Note)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to ~/.tokenops/events.db)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON array instead of text")
	return cmd
}

func newPlanCatalogCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "catalog",
		Short: "List every subscription plan TokenOps recognises",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, name := range plans.Names() {
				p, _ := plans.Lookup(name)
				fmt.Fprintf(cmd.OutOrStdout(), "%-22s %s (%s)\n", name, p.Display, p.Provider)
			}
			return nil
		},
	}
}

// storeReader adapts *sqlite.Store to the ports the domain and the
// capability layer declare, so neither imports sqlite directly.
type storeReader struct{ store *sqlite.Store }

// CountBySource satisfies headroom.Reader. The capability needs both
// reads, and taking them through one port is what stopped the CLI and
// the MCP server each carrying their own wrapper around this store.
func (s storeReader) CountBySource(ctx context.Context, since, until time.Time) (map[string]int64, error) {
	return s.store.CountBySource(ctx, since, until)
}

func (s storeReader) ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	return s.store.Query(ctx, sqlite.Filter{Type: t, Since: since, Limit: 100_000})
}

func resolvePlanDB(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if v := os.Getenv("TOKENOPS_STORAGE_PATH"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tokenops", "events.db"), nil
}

// spendSourceLabel says whose figure the spend line is, so an estimate is
// never read as the bill.
func spendSourceLabel(source string) string {
	if source == "vendor" {
		return "reported by the vendor"
	}
	return "estimated from token counts"
}

// sortedPlanProviders keeps `plan list` in a stable order.
func sortedPlanProviders(bindings map[string]string) []string {
	out := make([]string, 0, len(bindings))
	for p := range bindings {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
