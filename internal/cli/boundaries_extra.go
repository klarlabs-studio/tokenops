package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	bcap "go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/config"
)

// newBoundariesExtraCmd grants the operator a temporary extra allowance
// against boundaries, or ends one.
func newBoundariesExtraCmd() *cobra.Command {
	var (
		until, why string
		ruleNames  []string
		end        bool
		jsonOut    bool
	)
	cmd := &cobra.Command{
		Use:   "extra [duration]",
		Short: "Lift the headroom pause (or a named boundary) for a while: it binds again on its own",
		Long: `extra grants you a temporary extra allowance: the boundaries it names do
not ask or refuse until it ends, and then bind again on their own.

  tokenops boundaries extra 30m --why "finish the release"
  tokenops boundaries extra --until 18:00
  tokenops boundaries extra 2h --rule session-spend
  tokenops boundaries extra --end

Without --rule it lifts every rule of yours that pauses on plan headroom
(the headroom pause). It lasts at most 24 hours; for longer, change or demote
the rule. The grant is recorded as a decision with who granted it, why and
until when ('tokenops explain <id>'), and each session it lets through
records that it did. Status, the glance and the menu bar show it while it
holds. Your organisation's rules are not lifted here.

It is your word only: an agent cannot grant one. No MCP tool or API route
grants it, this command refuses to run when stdin is not a terminal, and
it is refused while boundary_approval is presence, since a grant cannot
be signed yet. --end only tightens, so it runs anywhere.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := presenceEnv(cmd)
			if err != nil {
				return err
			}
			if end {
				ended, err := bcap.EndExtras(env)
				if err != nil {
					return err
				}
				if jsonOut {
					return writeControlJSON(cmd, struct {
						Ended []bcap.ExtraView `json:"ended"`
					}{ended})
				}
				if len(ended) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No extra allowance was in force.")
				}
				for _, e := range ended {
					fmt.Fprintf(cmd.OutOrStdout(), "ended %s: %s bind again\n", e.ID, strings.Join(e.Rules, ", "))
				}
				return nil
			}
			if !approverAtTerminal(cmd.InOrStdin()) {
				return fmt.Errorf("tokenops boundaries extra needs a terminal: run it yourself, not through an agent")
			}
			req := bcap.ExtraRequest{Rules: ruleNames, Why: why, Method: bcap.MethodCLI}
			if len(args) == 1 {
				if req.For, err = time.ParseDuration(args[0]); err != nil || req.For <= 0 {
					return fmt.Errorf("%q is not a duration: say 30m, 2h or 1h30m", args[0])
				}
			}
			if until != "" {
				if req.Until, err = parseUntil(until, time.Now()); err != nil {
					return err
				}
			}
			path, err := resolveMutableConfigPath(configFlag(cmd))
			if err != nil {
				return err
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			res, err := bcap.GrantExtra(bcap.AllRules(cfg), env, req)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeControlJSON(cmd, res)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "extra allowance %s until %s\n%s\n", res.ID, res.Until.Local().Format("15:04"), res.Note)
			return nil
		},
	}
	cmd.Flags().StringVar(&until, "until", "", "when it ends: a clock time today or tomorrow (18:00), or an RFC 3339 time")
	cmd.Flags().StringSliceVar(&ruleNames, "rule", nil, "the boundary to lift (repeatable); default every rule that pauses on plan headroom")
	cmd.Flags().StringVar(&why, "why", "", "why, recorded on the decision")
	cmd.Flags().BoolVar(&end, "end", false, "end every extra allowance in force now")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

// parseUntil reads --until: a clock time, its next occurrence from now in
// the local time zone, or an RFC 3339 time.
func parseUntil(s string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	clock, err := time.ParseInLocation("15:04", s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("--until %q is not a clock time such as 18:00, or an RFC 3339 time", s)
	}
	local := now.Local()
	t := time.Date(local.Year(), local.Month(), local.Day(), clock.Hour(), clock.Minute(), 0, 0, time.Local)
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t, nil
}
