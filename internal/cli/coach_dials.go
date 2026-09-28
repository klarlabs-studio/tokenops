package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

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
	r := coachcap.Status(cfg, coachLedger(), time.Now())
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
		if p.Note != "" {
			fmt.Fprintf(out, "\n  %s: %s\n", p.Name, p.Note)
		}
		if p.Name == config.PowerModels && p.Effective == config.AutonomyAutonomous && !subagentHookInstalled() {
			fmt.Fprintln(out, "\n  models: the subagent hook is not installed, so nothing is moved yet; run `tokenops hooks install --route-guard`")
		}
	}
	activity := []string{}
	if s, err := coachhook.ReadStats(""); err == nil && s.Events > 0 {
		activity = append(activity, fmt.Sprintf("%d tips (%d on a quota window)", s.Alerts+totalOf(s.QuotaNudges)+s.PromotionNudges, totalOf(s.QuotaNudges)))
	}
	if n := coachcap.CountMoves(coachLedger(), config.PowerModels); n > 0 {
		activity = append(activity, fmt.Sprintf("%d subagents moved to a cheaper model", n))
	}
	if s, err := readguard.ReadStats(""); err == nil && s.Blocked > 0 {
		activity = append(activity, fmt.Sprintf("%d re-reads refused (~%dk tokens)", s.Blocked, s.ReclaimedTok/1000))
	}
	if len(activity) > 0 {
		fmt.Fprintf(out, "\n  lately: %s\n", strings.Join(activity, " · "))
	}
	renderFollowThrough(out, r)
	fmt.Fprintln(out, "\n  change: tokenops coach autonomy <off|advise|ask|autonomous> · verbosity <quiet|normal|verbose>")
	fmt.Fprintln(out, "          tokenops coach set <inform|waste|models> <rung> · tokenops coach off")
}

// subagentHookInstalled reports whether Claude Code has tokenops' route
// guard on the Agent tool, which is how models: autonomous acts.
func subagentHookInstalled() bool {
	settings, ok, err := loadSettings(resolveSettingsPath(""))
	if err != nil || !ok {
		return false
	}
	for _, loc := range findMarkerEntries(hooksMap(settings), "route-guard") {
		if loc.event == "PreToolUse" && loc.matcher == "Agent" {
			return true
		}
	}
	return false
}

// mutateCoach applies change to the config, validates the result, writes
// it, and prints the coach as it now stands.
func mutateCoach(cmd *cobra.Command, change func(*config.Config)) error {
	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	now := time.Now()
	l := coachLedger()
	if _, err := coachcap.Apply(path, l, now, change); err != nil {
		return err
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		return err
	}
	renderCoachStatus(cmd.OutOrStdout(), coachcap.Status(cfg, l, now))
	return nil
}

// renderFollowThrough shows what became of the coach's interventions:
// advice followed or ignored, moves that stood or were undone, and the
// kinds it has stopped offering because they kept being ignored.
func renderFollowThrough(out io.Writer, r coachcap.Report) {
	var lines []string
	for _, s := range r.FollowThrough {
		if s.Resolved() == 0 {
			continue
		}
		line := fmt.Sprintf("%s %s on %s work: ", s.Power, s.Channel, s.Kind)
		if s.Channel == "tip" {
			line = fmt.Sprintf("%s tips %s: ", s.Power, tipLabel(s.Kind))
		}
		if s.Channel == "move" {
			line += fmt.Sprintf("%d stood, %d undone", s.Stood, s.Undone)
		} else {
			line += fmt.Sprintf("%d followed, %d ignored", s.Followed, s.Ignored)
		}
		if s.Quiet {
			line += " · quiet now (ignored repeatedly; `verbosity verbose` still shows it)"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(out, "\n  follow-through:")
	for _, l := range lines {
		fmt.Fprintf(out, "    %s\n", l)
	}
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

// tipLabel says when a tip kind is given ("budget_50", "quota_90").
func tipLabel(kind string) string {
	if kind == "budget_over" {
		return "past the session budget"
	}
	for _, p := range []struct{ prefix, of string }{{"budget_", "the session budget"}, {"quota_", "a quota window"}} {
		if pct, ok := strings.CutPrefix(kind, p.prefix); ok {
			return "at " + pct + "% of " + p.of
		}
	}
	return "on " + kind
}
