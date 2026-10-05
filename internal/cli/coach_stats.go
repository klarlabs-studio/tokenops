package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

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
			out := cmd.OutOrStdout()
			if jsonOut {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(coachStats{Budget: hook, ReadGuard: guard})
			}
			writeCoachHookStats(out, hook)
			fmt.Fprintln(out)
			writeReadGuardStats(out, guard)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}
