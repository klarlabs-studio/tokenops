package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/savings"
)

// newCoachAcceptCmd answers the context power's proposal at the ask rung:
// the compaction line measured from the developer's own instructions.
func newCoachAcceptCmd() *cobra.Command {
	return newCoachAnswerCmd(true)
}

// newCoachDeclineCmd turns the proposal down; the same line is not put
// again.
func newCoachDeclineCmd() *cobra.Command {
	return newCoachAnswerCmd(false)
}

func newCoachAnswerCmd(accept bool) *cobra.Command {
	var (
		id      string
		jsonOut bool
	)
	use, short := "decline", "Turn down the compaction line the coach proposes"
	example := "  tokenops coach decline\n  tokenops coach decline --id 3f2a9c1e0b7d4a55"
	if accept {
		use, short = "accept", "Accept the compaction line the coach proposes, so the agents compact there"
		example = "  tokenops coach accept\n  tokenops coach accept --id 3f2a9c1e0b7d4a55 --json"
	}
	cmd := &cobra.Command{
		Use:     use,
		Short:   short,
		Example: example,
		Long: `With the context power at ask, the coach proposes where your agents
compact: the context line measured from your own instructions, the size
past which you reject more of what you are given. ` + "`tokenops coach`" + ` shows
the proposal.

accept writes it into each agent's settings, recording what it replaced
so lowering the power restores it. Until the compaction guardrail has
graduated (` + "`tokenops coach stats`" + ` shows its evidence), an accepted line is
recorded as what would be set and written once it graduates.

decline turns it down; the same line is not proposed again, and a line
that moves is.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			now := time.Now()
			l := coachLedger()
			var a coachcap.CompactAnswer
			var err error
			if accept {
				a, err = coachcap.AcceptCompaction(coachcap.AskEnv{
					Ledger: l, Shadow: savings.DefaultLedger(), Levers: contextLevers(),
					Graduated: func() bool { return coachcap.CompactionGraduated(l, now) },
				}, id, now)
			} else {
				a, err = coachcap.DeclineCompaction(l, id, now)
			}
			if errors.Is(err, coachcap.ErrNoProposal) {
				fmt.Fprintln(cmd.OutOrStdout(), "No compaction proposal to answer: the coach proposes one at the ask rung, after the daily waste report measures your context line.")
				return nil
			}
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(a)
			}
			writeCompactAnswer(cmd.OutOrStdout(), a)
			return nil
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "answer this proposal instead of the newest one")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

func writeCompactAnswer(out io.Writer, a coachcap.CompactAnswer) {
	switch {
	case !a.Accepted:
		fmt.Fprintf(out, "Declined the compaction line (%s): it is not proposed again.\n", a.Settings)
	case a.Written:
		fmt.Fprintf(out, "Accepted the compaction line (%s).\n", a.Settings)
		for _, r := range a.Results {
			fmt.Fprintf(out, "  %-11s %s = %d  %s\n", r.Client, r.Key, r.Value, r.Status)
		}
	default:
		fmt.Fprintf(out, "Accepted the compaction line (%s): %s.\n", a.Settings, a.Note)
	}
}

// renderProposal shows the compaction line waiting on the developer.
func renderProposal(out io.Writer, r coachcap.Report) {
	if r.Proposal == nil {
		return
	}
	fmt.Fprintf(out, "\n  context proposes: %s\n    accept with `%s`, or turn it down with `tokenops coach decline`\n", r.Proposal.Settings, r.Proposal.Command)
}
