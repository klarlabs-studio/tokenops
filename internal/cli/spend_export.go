package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/allocation"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/infra/planhistory"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// formatFocus is the only export format: FOCUS, as CSV.
const formatFocus = "focus"

// newSpendExportCmd builds `tokenops spend export`: spend and usage as a
// FOCUS dataset a FinOps tool ingests.
func newSpendExportCmd(rf *rootFlags) *cobra.Command {
	var (
		dbPath, sinceFlag, untilFlag, format, output string
		includeSrcs                                  []string
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export spend and usage as a FOCUS dataset (CSV) for FinOps tools",
		Long: `export writes the window's spend and usage as FOCUS 1.2 CSV, the FinOps
billing format that Vantage, CloudZero, Cloudability and other FinOps tools
ingest.

  - a plan's fee is a Purchase, prorated over the days it was in force
  - usage a plan covered is a Usage row billed 0, with ListCost at the
    API price, so ListCost against EffectiveCost is what the plan saved
  - usage billed per token is a Usage row at what it cost
  - tokens are the ConsumedQuantity
  - with allocation rules configured, the usage they tag is its own row,
    its project, client and cost_center in the Tags column

Only derived figures are written: providers, models, plans, counts, money
and your own allocation tags. No session title, path or prompt is in it.

The window defaults to the current month so far, in UTC days.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.ToLower(format) != formatFocus {
				return fmt.Errorf("--format %q is not supported; the export format is %q", format, formatFocus)
			}
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			path, err := resolveStorageReadPath(dbPath, cfg.Storage.Path)
			if err != nil {
				return err
			}
			q := spending.FocusQuery{Plans: cfg.Plans, Now: time.Now(), Allocation: &allocation.Deps{Rules: cfg.AllocationRules()}}
			if sinceFlag != "" {
				if q.Since, err = parseSince(sinceFlag); err != nil {
					return fmt.Errorf("--since: %w", err)
				}
			}
			if untilFlag != "" {
				if q.Until, err = time.Parse(time.RFC3339, untilFlag); err != nil {
					return fmt.Errorf("--until: %w", err)
				}
			}
			q.IncludeSources = resolveIncludeSources(cmd.ErrOrStderr(), includeSrcs)
			if file, err := planhistory.Default(); err == nil {
				if q.History, err = file.Load(); err != nil {
					return fmt.Errorf("read plan history: %w", err)
				}
			}
			ctx := cmd.Context()
			store, err := sqlite.Open(ctx, path, sqlite.Options{})
			if err != nil {
				return fmt.Errorf("open event store: %w", err)
			}
			defer func() { _ = store.Close() }()
			eng, err := buildSpendEngine(cfg)
			if err != nil {
				return err
			}
			d, err := spending.FocusExport(ctx, spending.NewAggregator(store, eng), q)
			if err != nil {
				return err
			}
			if err := writeFocus(cmd.OutOrStdout(), output, d); err != nil {
				return err
			}
			stderr := cmd.ErrOrStderr()
			if output != "" {
				fmt.Fprintf(stderr, "wrote %s: FOCUS %s, %d charge(s), %s – %s\n", output, d.Version, len(d.Rows),
					d.Since.Format(time.RFC3339), d.Until.Format(time.RFC3339))
			}
			for _, n := range d.Notes {
				fmt.Fprintf(stderr, "note: %s\n", n)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", formatFocus, "export format: focus (FOCUS 1.2, CSV)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "write to this file instead of stdout")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to config.storage.path)")
	cmd.Flags().StringVar(&sinceFlag, "since", "", "lower bound (RFC3339 or duration), floored to its UTC day; default the start of this month")
	cmd.Flags().StringVar(&untilFlag, "until", "", "upper bound (RFC3339 timestamp); default now")
	cmd.Flags().StringSliceVar(&includeSrcs, "include-source", nil,
		"re-admit an excluded event source (repeatable, comma-separated): mcp-session")
	return cmd
}

// writeFocus writes the dataset to path, or to stdout when path is empty.
func writeFocus(stdout io.Writer, path string, d spending.FocusDataset) error {
	if path == "" {
		return d.WriteCSV(stdout)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := d.WriteCSV(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
