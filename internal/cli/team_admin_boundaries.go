package cli

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// Organisation boundaries and approvals (ADR 0014 §4–5), administered
// through the same API as the web view.

func newTeamAdminBoundariesCmd(rf *rootFlags, af *adminFlags) *cobra.Command {
	var set string
	var clear bool
	cmd := &cobra.Command{
		Use:   "boundaries",
		Short: "Show the organisation's boundaries, or replace them from a YAML file",
		Long: `boundaries shows the rules your organisation sets on every member's
machine. They merge with each member's own rules and the strictest wins, so a
member can add boundaries but not loosen these.

--set replaces them with the rules in a YAML file, written as config.yaml's
boundaries section is (a top-level boundaries: key, or a list). Each rule's
stage (shadow or enforce) is yours to set here; members cannot change it.
An approver must be a role: member, lead, admin or owner. Owners and admins
only.`,
		Args: cobra.NoArgs,
		RunE: adminRun(rf, af, func(ctx context.Context, cmd *cobra.Command, a adminCtx, _ []string) error {
			var doc teamwire.OrgBoundaries
			var err error
			switch {
			case set != "" && clear:
				return fmt.Errorf("pass --set or --clear, not both")
			case set != "":
				rules, rerr := readBoundaryFile(set)
				if rerr != nil {
					return rerr
				}
				doc, err = a.client.SetOrgBoundaries(ctx, rules)
			case clear:
				doc, err = a.client.SetOrgBoundaries(ctx, nil)
			default:
				doc, err = a.client.OrgBoundaries(ctx)
			}
			if err != nil {
				return err
			}
			if af.json {
				return writeControlJSON(cmd, doc)
			}
			writeOrgBoundaries(cmd.OutOrStdout(), doc, set != "" || clear)
			return nil
		}),
	}
	cmd.Flags().StringVar(&set, "set", "", "replace the rules with those in this YAML file")
	cmd.Flags().BoolVar(&clear, "clear", false, "remove every organisation rule")
	return cmd
}

// readBoundaryFile reads rules as config.yaml writes them, and checks them
// as members' machines will parse them before anything is sent.
func readBoundaryFile(path string) ([]teamwire.BoundaryRule, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // a file the administrator named
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Boundaries []teamwire.BoundaryRule `yaml:"boundaries"`
	}
	var rules []teamwire.BoundaryRule
	if err := yaml.Unmarshal(raw, &wrapped); err == nil && len(wrapped.Boundaries) > 0 {
		rules = wrapped.Boundaries
	} else if err := yaml.Unmarshal(raw, &rules); err != nil {
		return nil, fmt.Errorf("%s: want a list of rules, or a boundaries: section: %w", path, err)
	}
	if err := teamwire.ValidateBoundaries(rules); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rules, nil
}

func writeOrgBoundaries(w io.Writer, doc teamwire.OrgBoundaries, changed bool) {
	if changed {
		fmt.Fprintf(w, "Saved as version %d; members' machines pick it up within five minutes.\n", doc.Version)
	}
	if len(doc.Rules) == 0 {
		fmt.Fprintln(w, "The organisation sets no boundaries.")
		return
	}
	fmt.Fprintf(w, "Version %d, updated %s\n", doc.Version, doc.UpdatedAt.Local().Format("2006-01-02 15:04"))
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tACTION\tWHEN\tREQUIRE\tSTAGE")
	for _, r := range doc.Rules {
		stage := r.Stage
		if stage == "" {
			stage = "shadow"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Name, r.Action, whenText(r.When), r.Require, stage)
	}
	_ = tw.Flush()
}

func whenText(when map[string]string) string {
	if len(when) == 0 {
		return "always"
	}
	keys := make([]string, 0, len(when))
	for k := range when {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+" "+when[k])
	}
	return strings.Join(parts, ", ")
}

func newTeamAdminApprovalsCmd(rf *rootFlags, af *adminFlags) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "approvals",
		Short: "List the approvals waiting on you: organisation rules that need your role",
		Args:  cobra.NoArgs,
		RunE: adminRun(rf, af, func(ctx context.Context, cmd *cobra.Command, a adminCtx, _ []string) error {
			list, err := a.client.Approvals(ctx, all)
			if err != nil {
				return err
			}
			if af.json {
				return writeControlJSON(cmd, list)
			}
			out := cmd.OutOrStdout()
			if len(list) == 0 {
				fmt.Fprintln(out, "Nothing is waiting on you.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tRULE\tMEMBER\tCROSSED\tNEEDS\tSTATUS\tASKED")
			for _, ap := range list {
				status := ap.Status
				if ap.AnsweredBy != "" {
					status += " by " + ap.AnsweredBy
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", ap.ID, ap.Rule, ap.Member, crossingsText(ap.Crossings),
					ap.Role, status, ap.RequestedAt.Local().Format("01-02 15:04"))
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			fmt.Fprintln(out, "\nAnswer with `tokenops team admin approve <id>` (--session for the rest of the session) or `deny <id>`.")
			return nil
		}),
	}
	cmd.Flags().BoolVar(&all, "all", false, "include answered and expired requests")
	return cmd
}

func newTeamAdminAnswerCmd(rf *rootFlags, af *adminFlags, verb string) *cobra.Command {
	var session bool
	short := "Approve a request an organisation rule sent to your role"
	if verb == "deny" {
		short = "Deny a request an organisation rule sent to your role"
	}
	cmd := &cobra.Command{
		Use:   verb + " <id>",
		Short: short,
		Long: short + `.

The id is the one ` + "`tokenops team admin approvals`" + ` lists, or the bnd-… id the
member's agent was refused with. A request you made yourself is answered by
someone else with the role, or by you in the web view: an agent on your
machine could use this command too.`,
		Args: cobra.ExactArgs(1),
		RunE: adminRun(rf, af, func(ctx context.Context, cmd *cobra.Command, a adminCtx, args []string) error {
			id := args[0]
			if teamwire.ValidDecisionID(id) {
				list, err := a.client.Approvals(ctx, false)
				if err != nil {
					return err
				}
				found := ""
				for _, ap := range list {
					if ap.DecisionID == id {
						found = ap.ID
					}
				}
				if found == "" {
					return fmt.Errorf("no request for %s is waiting on you (`tokenops team admin approvals` lists them)", id)
				}
				id = found
			}
			ap, err := a.client.AnswerApproval(ctx, id, teamwire.ApprovalAnswer{Decision: verb, Session: session})
			if err != nil {
				return err
			}
			if af.json {
				return writeControlJSON(cmd, ap)
			}
			scope := "the next step"
			if ap.Scope == teamwire.ScopeSession {
				scope = "the rest of the session"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s's request under %s, for %s. Their machine picks it up within a minute.\n",
				ap.Status, ap.Member, ap.Rule, scope)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&session, "session", false, "answer for the rest of the session (24 hours for a route or a destination), not only the next step")
	return cmd
}

// crossingsText renders what crossed a rule, as figures.
func crossingsText(cs []teamwire.Crossing) string {
	tiers := map[float64]string{1: "haiku", 2: "sonnet", 3: "opus"}
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		switch c.Field {
		case "model_tier":
			parts = append(parts, fmt.Sprintf("model %s %s %s", tiers[c.Value], c.Op, tiers[c.Threshold]))
		case "destination":
			parts = append(parts, "sharing with "+c.Destination)
		case "session_cost_usd":
			parts = append(parts, fmt.Sprintf("session $%.2f %s $%.2f", c.Value, c.Op, c.Threshold))
		case "plan_left_pct":
			parts = append(parts, fmt.Sprintf("plan left %.1f%% %s %g%% (%s)", c.Value, c.Op, c.Threshold, cmp.Or(c.Window, "tightest")))
		default:
			parts = append(parts, fmt.Sprintf("%s %.1f %s %g", c.Field, c.Value, c.Op, c.Threshold))
		}
	}
	return strings.Join(parts, ", ")
}
