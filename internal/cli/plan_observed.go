package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/checkup"
	"go.klarlabs.de/tokenops/internal/capability/planoverlay"
)

// newPlanObservedCmd shows, resets and turns on this machine's overlay of
// plan limits observed from its own window readings.
func newPlanObservedCmd(rf *rootFlags) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "observed",
		Short: "Plan limits your own windows show, which plan advice uses beside the catalog's",
		Long: `observed lists this machine's overlay of observed plan limits. When the
catalog-drift finding has enough evidence (five windows on each of two
tiers, filled 10–99%, one model family, a gap past 25% outside the
quartiles), the ratio your own windows imply is kept here, and plan advice
replays with it instead of the catalog's, saying so beside both figures.
The lower tier keeps its catalog figure; the higher tier is scaled to the
measured ratio.

It is this machine's only and never changes the shipped catalog: the
catalog changes by pull request (ADR 0002), so the finding still asks you
to open an issue. A limit is dropped on its own when the catalog changes
the figure it was observed against, or when the evidence no longer drifts.
'tokenops checkup' and the daemon (once a day) keep it.

  tokenops plan observed reset   go back to the catalog, and stop observing
  tokenops plan observed on      observe again, from your readings now`,
		Example: `  tokenops plan observed
  tokenops plan observed --json
  tokenops plan observed reset
  tokenops plan observed on`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := planoverlay.Show()
			if err != nil {
				return fmt.Errorf("read the observed limits: %w", err)
			}
			return writeObserved(cmd, v, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	cmd.AddCommand(newPlanObservedResetCmd(), newPlanObservedOnCmd(rf))
	return cmd
}

func writeObserved(cmd *cobra.Command, v planoverlay.View, jsonOut bool) error {
	if jsonOut {
		return writeControlJSON(cmd, v)
	}
	renderObserved(cmd.OutOrStdout(), v)
	return nil
}

func renderObserved(w io.Writer, v planoverlay.View) {
	switch {
	case v.Off:
		fmt.Fprintln(w, "Observed limits are off: plan advice uses the catalog. `tokenops plan observed on` observes again.")
		return
	case len(v.Limits) == 0:
		fmt.Fprintln(w, "No observed limits: your windows agree with the catalog, or there is not yet enough evidence (`tokenops checkup`).")
		return
	}
	fmt.Fprintln(w, "Plan advice on this machine uses these observed limits in place of the catalog's:")
	for _, l := range v.Limits {
		fmt.Fprintf(w, "  %s %s: observed %.1fx %s, catalog %gx (%d windows, %s, as of %s)\n",
			l.Display, l.Window, l.ObservedRatio, l.AnchorDisplay, l.CatalogRatio, l.Windows, l.ModelFamily,
			l.ObservedAt.Local().Format("2006-01-02"))
	}
	fmt.Fprintln(w, "The catalog is unchanged; if the vendor changed the limit, open an issue so it is fixed by pull request (ADR 0002).")
	fmt.Fprintln(w, "`tokenops plan observed reset` goes back to the catalog.")
}

func newPlanObservedResetCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:     "reset",
		Short:   "Go back to the catalog's limits, and stop observing until 'on'",
		Example: `  tokenops plan observed reset`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, err := planoverlay.Reset(time.Now())
			if err != nil {
				return err
			}
			if jsonOut {
				return writeControlJSON(cmd, v)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Observed limits cleared and off: plan advice uses the catalog. `tokenops plan observed on` observes again.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

func newPlanObservedOnCmd(rf *rootFlags) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:     "on",
		Short:   "Observe limits from your readings again, starting now",
		Example: `  tokenops plan observed on`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := checkup.Options{Now: time.Now()}
			closeStore := withDriftSources(cmd.Context(), rf, &opts)
			defer closeStore()
			v, err := planoverlay.TurnOn(cmd.Context(), opts)
			if err != nil {
				return err
			}
			return writeObserved(cmd, v, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}
