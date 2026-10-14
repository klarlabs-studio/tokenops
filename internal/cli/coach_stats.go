package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	"go.klarlabs.de/tokenops/internal/infra/readguard"
)

// coachStats is `tokenops coach stats --json`.
type coachStats struct {
	// Budget is the Stop hook's ledger: session spend against budget
	// fractions, quota and compact tips.
	Budget coachhook.Stats `json:"budget"`
	// ReadGuard is the read guard's ledger: repeat reads, reclaimable and
	// refused.
	ReadGuard readguard.Stats `json:"read_guard"`
	// Savings is the realized-savings ledger: every intervention with
	// what it was expected to save, and what the holdout showed it saved.
	Savings savings.Report `json:"savings"`
}

// writeSavings prints the realized-savings ledger: expected next to
// realized, so a potential saving is never read as a delivered one.
func writeSavings(out io.Writer, r savings.Report) {
	if len(r.Kinds) == 0 {
		return
	}
	fmt.Fprintln(out, "\nSavings ledger — expected when acting, realized as the holdout measured it")
	for _, k := range r.Kinds {
		fmt.Fprintf(out, "  %s\n", savings.Describe(k))
	}
	if total, ok := r.RealizedUSD.Amount(); ok {
		fmt.Fprintf(out, "  realized in total: $%.2f API-equivalent\n", total)
	}
}

// newCoachStatsCmd is what the coach saw and did, from the hooks' ledgers.
func newCoachStatsCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "What the coach saw and did: budget tips, re-reads, what it held back",
		Long: `stats reads the coach's two ledgers. The Stop hook's: each session's
spend against budget fractions, quota and compact tips, and what it held
back. The read guard's: files read again, which re-reads were waste, and
which it refused. Both keep their ledgers while the coach only observes, so
this shows what you would hear before you let it speak.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			hook, err := coachhook.ReadStats("")
			if err != nil {
				return err
			}
			guard, err := readguard.ReadStats("")
			if err != nil {
				return err
			}
			saved := savings.Build(cmd.Context(), savings.DefaultDeps(), time.Now())
			out := cmd.OutOrStdout()
			if jsonOut {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(coachStats{Budget: hook, ReadGuard: guard, Savings: saved})
			}
			writeCoachHookStats(out, hook)
			fmt.Fprintln(out)
			writeReadGuardStats(out, guard)
			writeSavings(out, saved)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}
