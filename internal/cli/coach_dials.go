package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	"go.klarlabs.de/tokenops/internal/infra/readguard"
)

// coachStatus is `tokenops coach` with no subcommand: the coach's two
// dials, each power's configured and effective autonomy with the reason
// and the setting it came from, and what the coach has done lately.
func coachStatus(cmd *cobra.Command, jsonOut bool) error {
	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		return err
	}
	r := coachcap.Build(cfg)
	if jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	renderCoachStatus(cmd.OutOrStdout(), r)
	return nil
}

func renderCoachStatus(out io.Writer, r coachcap.Report) {
	state := "on"
	if r.Off() {
		state = "off (records, says nothing)"
	}
	fmt.Fprintf(out, "coach: %s · verbosity %s (%s)\n\n", state, r.Verbosity, r.VerbositySource)
	fmt.Fprintf(out, "  %-7s %-11s %-11s %s\n", "POWER", "CONFIGURED", "EFFECTIVE", "SET BY")
	for _, p := range r.Powers {
		fmt.Fprintf(out, "  %-7s %-11s %-11s %s\n", p.Name, p.Configured, p.Effective, p.Source)
	}
	for _, p := range r.Powers {
		if p.Reason != "" {
			fmt.Fprintf(out, "\n  %s is %s, not %s: %s\n", p.Name, p.Effective, p.Configured, p.Reason)
		}
	}
	activity := []string{}
	if s, err := coachhook.ReadStats(""); err == nil && s.Events > 0 {
		activity = append(activity, fmt.Sprintf("%d tips (%d on a quota window)", s.Alerts+totalOf(s.QuotaNudges)+s.PromotionNudges, totalOf(s.QuotaNudges)))
	}
	if s, err := readguard.ReadStats(""); err == nil && s.Blocked > 0 {
		activity = append(activity, fmt.Sprintf("%d re-reads refused (~%dk tokens)", s.Blocked, s.ReclaimedTok/1000))
	}
	if len(activity) > 0 {
		fmt.Fprintf(out, "\n  lately: %s\n", strings.Join(activity, " · "))
	}
	fmt.Fprintln(out, "\n  change: tokenops coach autonomy <off|advise|ask|autonomous> · verbosity <quiet|normal|verbose>")
	fmt.Fprintln(out, "          tokenops coach set <inform|waste|models> <rung> · tokenops coach off")
}

// mutateCoach applies change to the config, validates the result, writes
// it, and prints the coach as it now stands.
func mutateCoach(cmd *cobra.Command, change func(*config.Config)) error {
	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		return err
	}
	change(&cfg)
	if err := cfg.Coach.Validate(); err != nil {
		return err
	}
	if err := config.WriteMutable(path, cfg); err != nil {
		return err
	}
	renderCoachStatus(cmd.OutOrStdout(), coachcap.Build(cfg))
	return nil
}

func newCoachAutonomyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "autonomy [off|advise|ask|autonomous]",
		Short: "Show or set who decides: every power's default autonomy",
		Long: `autonomy decides who acts when the coach sees something worth changing:

  off         records what it would say or do, and does neither
  advise      tells you what you could do better; changes nothing
  ask         steps in with a concrete change and waits for your approval
  autonomous  makes the change itself

It is the default for every power (inform, waste, models); ` + "`tokenops coach set`" + `
overrides one. A rung the coach cannot deliver here yet is shown one rung
lower, with the reason. Takes effect on the next hook invocation.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return coachStatus(cmd, false)
			}
			return mutateCoach(cmd, func(c *config.Config) { c.Coach.Autonomy = args[0] })
		},
	}
}

func newCoachVerbosityCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verbosity [quiet|normal|verbose]",
		Short: "Show or set how much the coach says",
		Long: `verbosity decides how much the coach says, independent of who decides:

  quiet    only when work is about to stop
  normal   each tip once, with one suggestion (the default)
  verbose  every tip, with the reasoning

Approval requests are always shown.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return coachStatus(cmd, false)
			}
			return mutateCoach(cmd, func(c *config.Config) { c.Coach.Verbosity = args[0] })
		},
	}
}

func newCoachSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <inform|waste|models> <off|advise|ask|autonomous>",
		Short: "Set one power's autonomy, overriding the default",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mutateCoach(cmd, func(c *config.Config) {
				if c.Coach.Powers == nil {
					c.Coach.Powers = map[string]string{}
				}
				c.Coach.Powers[strings.ToLower(strings.TrimSpace(args[0]))] = args[1]
			})
		},
	}
}

func newCoachOffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "off",
		Short: "Turn the coach off: it records, and says and does nothing",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return mutateCoach(cmd, func(c *config.Config) {
				c.Coach.Autonomy = config.AutonomyOff
				c.Coach.Powers = nil
			})
		},
	}
}

func newCoachMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Write a coach block equivalent to your current settings",
		Long: `migrate writes coach.powers and coach.verbosity with exactly what the
older coaching.delivery and optimizer.smart_routing keys resolve to today,
so behaviour does not change. The older keys are left in place.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return mutateCoach(cmd, func(c *config.Config) {
				powers := map[string]string{}
				for _, p := range config.Powers() {
					powers[p] = c.CoachPower(p).Rung
				}
				v, _ := c.CoachVerbosity()
				c.Coach = config.CoachConfig{Verbosity: v, Powers: powers}
			})
		},
	}
}

// deliveryOverridden reports why coaching.delivery no longer decides a
// power, or "" when it still does.
func deliveryOverridden(cfg config.Config) string {
	for _, p := range []string{config.PowerInform, config.PowerWaste} {
		if src := cfg.CoachPower(p).Source; src != "coaching.delivery" {
			return src
		}
	}
	return ""
}
