package cli

import (
	"context"
	"encoding/json"
	"fmt"
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
	cmd.AddCommand(newPlanListCmd(rf), newPlanHeadroomCmd(rf), newPlanCatalogCmd())
	return cmd
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
				fmt.Fprintln(cmd.OutOrStdout(), "no plans configured; set `plans:` in config or TOKENOPS_PLAN_<PROVIDER> env vars")
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
				report, err := plans.ComputeHeadroom(planName, plans.HeadroomInputs{
					ConsumedTokens: cons.ConsumedTokens,
					Last7DayTokens: cons.Last7DayTokens,
					Now:            now,
				})
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
					"%s (%s): consumed %d tokens (%.1f%%) — risk %s\n",
					r.Display, r.PlanName, r.ConsumedTokens, r.ConsumedPct, r.OverageRisk,
				)
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
