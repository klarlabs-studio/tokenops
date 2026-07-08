package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/pricing"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
)

// buildSpendEngine constructs the effective-dated cost engine for CLI
// commands that price events outside the daemon bootstrap (spend, replay):
// events are priced at the rate card in effect at their timestamp, from the
// embedded baseline plus persisted snapshots under ~/.tokenops/pricing, with
// the negotiated-rate override (cfg.Pricing.Path) layered across every
// period. Fail-soft: any error building the effective-dated engine degrades
// to the flat baseline+override engine so costing never breaks. A malformed
// override file is a hard error, surfaced to the caller as before.
func buildSpendEngine(cfg config.Config) (*spend.Engine, error) {
	flatTable, err := spend.TableWithOverrides(cfg.Pricing.Path)
	if err != nil {
		return nil, err
	}
	fallback := spend.NewEngine(flatTable)

	overrides := spend.Table{}
	if cfg.Pricing.Path != "" {
		ov, oerr := spend.LoadTableFile(cfg.Pricing.Path)
		if oerr != nil {
			return fallback, nil
		}
		overrides = ov
	}

	eng, eerr := pricing.EffectiveEngineWithOverrides("", overrides)
	if eerr != nil || eng == nil {
		return fallback, nil
	}
	return eng, nil
}

// newPricingCmd builds the `tokenops pricing` command tree: the ADR 0002
// pricing-research framework. It fetches sourced, timestamped rate snapshots,
// diffs them so drift is loud, and lints them for the family-ratio anomalies
// that hid the Opus ⅓ error. As of Phase 2 the cost engine consults these
// snapshots: events are priced at the rate card in effect at their timestamp
// (see buildSpendEngine / pricing.EffectiveEngine).
func newPricingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pricing",
		Short: "Research, snapshot, diff, and lint model pricing (ADR 0002)",
		Long: `pricing manages sourced, timestamped pricing snapshots.

Instead of one hand-maintained rate table that drifts silently, pricing
fetches rates from a pluggable source (default: LiteLLM), stores each fetch as
a timestamped snapshot with provenance under ~/.tokenops/pricing/snapshots/,
and DIFFS every refresh against the previous snapshot so a change like
"anthropic/claude-opus-4-8 cache_read 0.50 → 1.50 (+200%)" shouts instead of
hiding. Snapshots cover every provider the catalog prices (rates are keyed
"<provider>/<model>"). A consistency guard lints each snapshot; its family-ratio
check (cache-read ≈10% of input, output ≈5× input) is an Anthropic-family
invariant and runs only on anthropic/* rows — the exact check that caught Opus.

The embedded pricing.yaml is always available as the offline baseline. See
docs/pricing-research.md.`,
	}
	cmd.AddCommand(
		newPricingRefreshCmd(),
		newPricingShowCmd(),
		newPricingDiffCmd(),
		newPricingLintCmd(),
	)
	return cmd
}

func newPricingRefreshCmd() *cobra.Command {
	var (
		source string
		url    string
		dir    string
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Fetch the latest rates, lint + diff them, and write a snapshot",
		Long: `refresh fetches the current rate card from the source, runs the
consistency guard (anomalies print as warnings), diffs it against the latest
existing snapshot (or the baseline), prints the changes, and writes the new
snapshot — unless --dry-run.

The fetch reaches the network. In a sandboxed environment that blocks outbound
calls (the same limit as provider live-verify), run refresh where the network
is reachable (an operator machine or CI). On any fetch error, refresh prints a
clear message and exits non-zero WITHOUT writing a snapshot; offline callers
keep working on the baseline.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()

			src := pricing.SourceByName(source, url)
			if src == nil {
				return fmt.Errorf("unknown pricing source %q (known: litellm)", source)
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			fmt.Fprintf(out, "Fetching rates from %s…\n", src.Name())
			snap, err := src.Fetch(ctx)
			if err != nil {
				fmt.Fprintf(errOut, "pricing refresh: fetch failed: %v\n", err)
				fmt.Fprintln(errOut, "No snapshot written. If this environment blocks outbound network, run refresh where the source is reachable (operator machine or CI).")
				return err
			}
			fmt.Fprintf(out, "Fetched %d model rates (as of %s).\n\n", len(snap.Rates), snap.FetchedAt.Format(time.RFC3339))

			// Consistency guard — warn, never block.
			if anomalies := pricing.Check(snap); len(anomalies) > 0 {
				fmt.Fprintf(errOut, "⚠ consistency guard flagged %d anomaly(ies) in the fetched rates:\n", len(anomalies))
				for _, a := range anomalies {
					fmt.Fprintf(errOut, "  - %s\n", a.String())
				}
				fmt.Fprintln(errOut, "  (fetched anyway — review before trusting these rates.)")
				fmt.Fprintln(errOut)
			}

			// Diff against the latest snapshot (or baseline when none exists).
			prev, real := pricing.LatestSnapshot(dir)
			label := "baseline"
			if real {
				label = prev.FetchedAt.Format(time.RFC3339)
			}
			changes := pricing.Diff(prev, snap)
			if len(changes) == 0 {
				fmt.Fprintf(out, "No changes vs %s (%s).\n", label, prev.Source)
			} else {
				fmt.Fprintf(out, "Changes vs %s (%s):\n", label, prev.Source)
				for _, c := range changes {
					fmt.Fprintf(out, "  %s\n", pricing.FormatChange(c))
				}
			}

			if dryRun {
				fmt.Fprintln(out, "\n--dry-run: snapshot not written.")
				return nil
			}
			path, err := pricing.SaveSnapshot(dir, snap)
			if err != nil {
				return fmt.Errorf("write snapshot: %w", err)
			}
			fmt.Fprintf(out, "\nSnapshot written: %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&source, "source", "litellm", "pricing source (litellm)")
	cmd.Flags().StringVar(&url, "url", "", "override the source URL (default: source's built-in)")
	cmd.Flags().StringVar(&dir, "dir", "", "pricing state dir (default: ~/.tokenops/pricing)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "fetch, lint, and diff but do not write the snapshot")
	return cmd
}

func newPricingShowCmd() *cobra.Command {
	var (
		snapshot string
		dir      string
		jsonOut  bool
	)
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the rates in a snapshot (default: latest, else baseline)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			snap, err := pricing.FindSnapshot(dir, snapshot)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if jsonOut {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(snap)
			}
			fmt.Fprintf(out, "source: %s", snap.Source)
			if snap.SourceURL != "" {
				fmt.Fprintf(out, " (%s)", snap.SourceURL)
			}
			fmt.Fprintf(out, "\nfetched_at: %s\n", snap.FetchedAt.Format(time.RFC3339))
			fmt.Fprintf(out, "models: %d\n\n", len(snap.Rates))
			fmt.Fprintf(out, "%-34s %12s %12s %12s\n", "PROVIDER/MODEL", "INPUT", "OUTPUT", "CACHE_READ")
			// Models() returns "<provider>/<model>" keys sorted lexically, so
			// rows already group by provider.
			pinned := pricing.PinnedSnapshotKeys()
			anyPinned := false
			for _, m := range snap.Models() {
				r := snap.Rates[m]
				mark := ""
				if pinned[m] {
					mark = "  [pinned]"
					anyPinned = true
				}
				fmt.Fprintf(out, "%-34s %12.4g %12.4g %12.4g%s\n", m, r.InputPerMillion, r.OutputPerMillion, r.CachedInputPerMillion, mark)
			}
			if anyPinned {
				fmt.Fprintln(out, "\n[pinned] = verified catalog row; the cost engine prices it at the baseline, so the value above is source provenance only and may differ from what costing uses.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&snapshot, "snapshot", "latest", "snapshot selector: latest | baseline | <RFC3339 timestamp>")
	cmd.Flags().StringVar(&dir, "dir", "", "pricing state dir (default: ~/.tokenops/pricing)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the snapshot as JSON")
	return cmd
}

func newPricingDiffCmd() *cobra.Command {
	var (
		from string
		to   string
		dir  string
	)
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Diff two snapshots (default: baseline → latest)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			oldSnap, err := pricing.FindSnapshot(dir, from)
			if err != nil {
				return fmt.Errorf("--from %q: %w", from, err)
			}
			newSnap, err := pricing.FindSnapshot(dir, to)
			if err != nil {
				return fmt.Errorf("--to %q: %w", to, err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s (%s)  →  %s (%s)\n",
				oldSnap.FetchedAt.Format(time.RFC3339), oldSnap.Source,
				newSnap.FetchedAt.Format(time.RFC3339), newSnap.Source)
			changes := pricing.Diff(oldSnap, newSnap)
			if len(changes) == 0 {
				fmt.Fprintln(out, "no changes.")
				return nil
			}
			pinned := pricing.PinnedSnapshotKeys()
			anyPinned := false
			for _, c := range changes {
				line := pricing.FormatChange(c)
				if pinned[c.Model] {
					line += "  [pinned: runtime keeps baseline]"
					anyPinned = true
				}
				fmt.Fprintf(out, "  %s\n", line)
			}
			if anyPinned {
				fmt.Fprintln(out, "\n[pinned] = verified catalog row; the cost engine ignores the source here and prices at the baseline. Do not \"correct\" the baseline toward this drift without re-checking the vendor.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "baseline", "old snapshot: baseline | latest | <RFC3339 timestamp>")
	cmd.Flags().StringVar(&to, "to", "latest", "new snapshot: latest | baseline | <RFC3339 timestamp>")
	cmd.Flags().StringVar(&dir, "dir", "", "pricing state dir (default: ~/.tokenops/pricing)")
	return cmd
}

func newPricingLintCmd() *cobra.Command {
	var (
		snapshot string
		dir      string
	)
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Run the consistency guard on a snapshot (exit non-zero on anomalies)",
		Long: `lint runs the consistency guard over a snapshot and reports any
anomalies. The family-ratio check (cache-read ≈10% of input, output ≈5× input)
is Anthropic-family-specific and runs only on anthropic/* rows; all rows also
get a conservative generic sanity check (e.g. cache-read must not exceed input).
It exits non-zero when anomalies are found, so it can gate CI. Default snapshot:
latest, falling back to the baseline.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			snap, err := pricing.FindSnapshot(dir, snapshot)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			anomalies := pricing.Check(snap)
			if len(anomalies) == 0 {
				fmt.Fprintf(out, "OK — %d models, no consistency anomalies (%s @ %s).\n",
					len(snap.Rates), snap.Source, snap.FetchedAt.Format(time.RFC3339))
				return nil
			}
			fmt.Fprintf(out, "%d consistency anomaly(ies) in %s @ %s:\n",
				len(anomalies), snap.Source, snap.FetchedAt.Format(time.RFC3339))
			for _, a := range anomalies {
				fmt.Fprintf(out, "  - %s\n", a.String())
			}
			return fmt.Errorf("pricing lint: %d anomaly(ies) found", len(anomalies))
		},
	}
	cmd.Flags().StringVar(&snapshot, "snapshot", "latest", "snapshot selector: latest | baseline | <RFC3339 timestamp>")
	cmd.Flags().StringVar(&dir, "dir", "", "pricing state dir (default: ~/.tokenops/pricing)")
	return cmd
}
