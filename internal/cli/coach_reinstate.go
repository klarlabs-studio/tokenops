package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
)

// newCoachReinstateCmd lets the operator restore a model move the coach
// retracted because its quality dropped, or a tip kind it retired because
// it was ignored whenever it was seen.
func newCoachReinstateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reinstate <kind|all>",
		Short: "Let the coach make a move it retracted, or give a tip it retired, again",
		Long: `When work the coach moved to a cheaper model did measurably worse than
comparable work it did not move, the coach retracts that kind of move:
it stops making it on its own and advises instead. ` + "`tokenops explain moves`" + `
shows the evidence.

reinstate overrides that on your word. The kind is the task kind
(edit, deep, research, lookup) or default_model for the model new
sessions start on; all reinstates every retracted move. New evidence can
also reinstate a move on its own: work of that kind run on the cheaper
model since the retraction that holds quality.

It also brings back a tip kind the coach retired. A kind of tip or advice
given at a decision point that was seen at least 20 times in the last 30
days and ignored 80% of those times or more is retired: the coach stops
giving it. ` + "`tokenops coach`" + ` lists each retired kind with the figures it rests
on. reinstate takes the kind (reread_churn, search_loop, subagent_model,
window_fit, model_fit, lookup, …); the evidence before it no longer counts.`,
		Example: "  tokenops coach reinstate edit\n  tokenops coach reinstate default_model\n  tokenops coach reinstate reread_churn\n  tokenops coach reinstate all",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := strings.TrimSpace(args[0])
			now := time.Now()
			ended, err := coachcap.Reinstate(coachcap.MoveStore(), kind, now)
			if err != nil {
				return err
			}
			tips, err := coachcap.ReinstateKind(coachLedger(), now, kind)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(ended) == 0 && len(tips) == 0 {
				fmt.Fprintf(out, "No retracted move or retired tip for %q; `tokenops explain moves` and `tokenops coach` list them.\n", kind)
				return nil
			}
			for _, r := range ended {
				fmt.Fprintf(out, "Reinstated %s → %s for %s (decision %s). The coach may make it on its own again.\n", r.Key.From, r.Key.To, r.Key.Kind, r.ID)
			}
			for _, r := range tips {
				fmt.Fprintf(out, "Reinstated %s %s, retired after it was %s. The coach may give it again.\n", r.Power, r.Kind, r.Why())
			}
			return nil
		},
	}
}
