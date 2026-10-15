package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/anomalies"
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
)

// tipAnswers maps the words the command takes to the answers it records.
var tipAnswers = map[string]string{
	"ack": coachcap.AnswerAcknowledged, "acknowledge": coachcap.AnswerAcknowledged,
	"expected": coachcap.AnswerExpected,
	"snooze":   coachcap.AnswerSnoozed,
}

// newCoachTipCmd answers the coach's tips: the lifecycle's last step.
func newCoachTipCmd() *cobra.Command {
	var (
		id  string
		dur time.Duration
	)
	cmd := &cobra.Command{
		Use:   "tip ack|expected|snooze",
		Short: "Answer the coach's newest tip: acknowledge it, call it expected, or snooze it",
		Long: `tip answers a tip the coach gave, by default the newest one you have not
answered (--id names another; the ids are in 'tokenops coach --json').

  ack       you have seen it. The coach says nothing more of that kind in
            the session: the 75% budget tip, then 100%, then 200% are one
            episode, told once.
  expected  this is normal for you. It closes the episode the same way and
            is recorded as an outcome: once most of your answered budget
            tips are "expected", the session budget is read off your p95
            session instead of your p90.
  snooze    not now. Tips of that kind wait for --for (default 4h), in
            every session. A warning that a plan window will run out, or is
            used up, still speaks.

An unusual-spend finding (its id starts with anomaly-) is answered the
same way: ack takes it off the findings, expected also stops flagging the
same pattern (the same pricier model, the same plan billed per token, a
loop in the same repository), and snooze holds every spend anomaly.

An answered tip counts as neither followed nor ignored. ` + "`tokenops coach`" + ` shows
how many tips of each kind were answered and how many were snoozed.`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"ack", "expected", "snooze"},
		RunE: func(cmd *cobra.Command, args []string) error {
			answer, ok := tipAnswers[args[0]]
			if !ok {
				return fmt.Errorf("unknown answer %q: use ack, expected or snooze", args[0])
			}
			r, err := coachcap.AnswerTip(coachLedger(), time.Now(), id, answer, dur)
			if errors.Is(err, coachcap.ErrNoTip) {
				fmt.Fprintln(cmd.OutOrStdout(), "No tip to answer: the coach has given none you have not answered.")
				return nil
			}
			if err != nil {
				return err
			}
			writeTipAnswer(cmd.OutOrStdout(), r)
			return nil
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "answer this tip instead of the newest unanswered one")
	cmd.Flags().DurationVar(&dur, "for", coachcap.DefaultSnooze, "how long a snooze lasts")
	return cmd
}

func writeTipAnswer(out io.Writer, r coachcap.TipAnswer) {
	if cause, ok := strings.CutPrefix(r.Kind, anomalies.KindPrefix); ok {
		writeAnomalyAnswer(out, r, cause)
		return
	}
	what := tipLabel(r.Kind)
	switch {
	case r.Answer == coachcap.AnswerSnoozed && r.SnoozedUntil != nil:
		fmt.Fprintf(out, "Snoozed the tip %s: tips like it wait until %s.\n", what, r.SnoozedUntil.Local().Format("Mon 15:04"))
	case r.Answer == coachcap.AnswerExpected:
		fmt.Fprintf(out, "Recorded the tip %s as expected: nothing more like it this session, and it counts toward how your session budget is set.\n", what)
	default:
		fmt.Fprintf(out, "Acknowledged the tip %s: nothing more like it this session.\n", what)
	}
}

// anomalyCauses words an anomaly's cause for an answer.
var anomalyCauses = map[string]string{
	"pricier_model":   "a pricier model",
	"metered_on_plan": "usage billed per token under a plan",
	"runaway_loop":    "a likely runaway loop",
	"spike":           "a spike",
}

// writeAnomalyAnswer confirms an answer to an unusual-spend finding.
func writeAnomalyAnswer(out io.Writer, r coachcap.TipAnswer, cause string) {
	what := "the unusual spend"
	if c, ok := anomalyCauses[cause]; ok {
		what += " (" + c + ")"
	}
	switch {
	case r.Answer == coachcap.AnswerSnoozed && r.SnoozedUntil != nil:
		fmt.Fprintf(out, "Snoozed %s: unusual spend is not flagged until %s.\n", what, r.SnoozedUntil.Local().Format("Mon 15:04"))
	case r.Answer == coachcap.AnswerExpected:
		fmt.Fprintf(out, "Recorded %s as expected: the same pattern is no longer flagged.\n", what)
	default:
		fmt.Fprintf(out, "Acknowledged %s: it leaves the findings.\n", what)
	}
}
