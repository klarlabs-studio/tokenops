package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
)

// newCoachReinstateCmd lets the operator restore a model move the coach
// retracted because its quality dropped.
func newCoachReinstateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reinstate <kind|all>",
		Short: "Let the coach make a model move it retracted again",
		Long: `When work the coach moved to a cheaper model did measurably worse than
comparable work it did not move, the coach retracts that kind of move:
it stops making it on its own and advises instead. ` + "`tokenops explain moves`" + `
shows the evidence.

reinstate overrides that on your word. The kind is the task kind
(edit, deep, research, lookup) or default_model for the model new
sessions start on; all reinstates every retracted move. New evidence can
also reinstate a move on its own: work of that kind run on the cheaper
model since the retraction that holds quality.`,
		Example: "  tokenops coach reinstate edit\n  tokenops coach reinstate default_model\n  tokenops coach reinstate all",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := strings.TrimSpace(args[0])
			ended, err := coachcap.Reinstate(coachcap.MoveStore(), kind, time.Now())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(ended) == 0 {
				fmt.Fprintf(out, "No retracted move for %q; `tokenops explain moves` lists them.\n", kind)
				return nil
			}
			for _, r := range ended {
				fmt.Fprintf(out, "Reinstated %s → %s for %s (decision %s). The coach may make it on its own again.\n", r.Key.From, r.Key.To, r.Key.Kind, r.ID)
			}
			return nil
		},
	}
}
