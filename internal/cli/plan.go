package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/plans"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
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
	var configPathFlag string
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
			if err := plans.Validate(planName); err != nil {
				return err
			}
			path, err := resolveMutableConfigPath(configPathFlag)
			if err != nil {
				return err
			}
			cfg, err := readMutableConfig(path)
			if err != nil {
				return err
			}
			if cfg.Plans == nil {
				cfg.Plans = map[string]string{}
			}
			previous, existed := cfg.Plans[provider]
			cfg.Plans[provider] = planName
			if err := writeMutableConfig(path, cfg); err != nil {
				return err
			}
			if existed && previous != planName {
				fmt.Fprintf(cmd.OutOrStdout(), "updated plans.%s: %s -> %s\n", provider, previous, planName)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "set plans.%s = %s\n", provider, planName)
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"wrote %s\nnext: reload your MCP server (or restart the daemon) to pick up the change\n",
				path,
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
	return cmd
}

func newPlanUnsetCmd() *cobra.Command {
	var configPathFlag string
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
				"removed plans.%s\nwrote %s\nnext: reload your MCP server\n",
				provider, path,
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPathFlag, "config-path", "", "override config file path")
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
			for provider, planName := range cfg.Plans {
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
				return fmt.Errorf("no plans configured; set `plans:` in config or TOKENOPS_PLAN_<PROVIDER>")
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

			reader := storeReader{store: store}
			reports := make([]plans.HeadroomReport, 0, len(cfg.Plans))
			now := time.Now().UTC()
			for provider, planName := range cfg.Plans {
				cons, err := plans.ConsumptionFor(ctx, reader, provider, now)
				if err != nil {
					return fmt.Errorf("consumption[%s]: %w", provider, err)
				}
				inputs := plans.HeadroomInputs{
					ConsumedTokens: cons.ConsumedTokens,
					Last7DayTokens: cons.Last7DayTokens,
					Now:            now,
				}
				if p, ok := plans.Lookup(planName); ok && p.RateLimitWindow > 0 {
					win, err := plans.ConsumptionInWindow(ctx, reader, provider, now, p.RateLimitWindow)
					if err != nil {
						return fmt.Errorf("window[%s]: %w", provider, err)
					}
					inputs.WindowMessages = win.MessagesInWindow
				}
				report, err := plans.ComputeHeadroom(planName, inputs)
				if err != nil {
					return fmt.Errorf("headroom[%s]: %w", provider, err)
				}
				reports = append(reports, report)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(reports)
			}
			for _, r := range reports {
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
				} else {
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

// storeReader adapts *sqlite.Store to the plans.EventReader port so the
// domain package never imports sqlite directly.
type storeReader struct{ store *sqlite.Store }

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
