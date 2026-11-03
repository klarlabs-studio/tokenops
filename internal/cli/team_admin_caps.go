package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/orgcaps"
	anthropicapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/anthropic"
	copilotapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/copilot"
)

// The vendors' caps on each member, read with the administrator's own
// vendor keys on this machine (gap G7).

func newTeamAdminCapsCmd(rf *rootFlags, af *adminFlags) *cobra.Command {
	var githubOrg string
	var useGH bool
	cmd := &cobra.Command{
		Use:   "caps",
		Short: "Each member's vendor spend cap beside the team: Claude Enterprise spend limits and Copilot budgets",
		Long: `caps reads the spend caps your vendors set, the ceiling your organisation's
boundaries sit under: a boundary can ask or refuse before a member reaches a
cap, not lift it.

Claude Enterprise: each member's spend limit, where it was inherited from
(set for them, their seat tier, a group or the organisation), the spend so
far in its period, and any request for a higher limit waiting for an admin.
It reads with vendor_usage.anthropic.analytics_key
(TOKENOPS_ANTHROPIC_ANALYTICS_KEY), a key the primary owner creates in
claude.ai > Organization settings > API with the read:spend_limits scope
(one key can carry read:analytics beside it). Members are matched to the team
by e-mail address. Requests are approved or denied in claude.ai.

Copilot, with --github-org: the organisation's budgets on Copilot, its
premium requests or AI credits, for the organisation, a cost center, a
repository or a user, and whether each stops usage or only alerts. It reads
with GH_TOKEN or GITHUB_TOKEN, or with --gh the GitHub CLI's token, as an
owner or billing manager of the organisation. A user budget names a GitHub
login, matched to the team member of that name.

Keys stay on this machine; nothing is uploaded or kept. OpenAI's per-seat
credit limits are set only in its billing settings, with no API to read
them.`,
		Args: cobra.NoArgs,
		RunE: adminRun(rf, af, func(ctx context.Context, cmd *cobra.Command, a adminCtx, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			key := cfg.VendorUsage.Anthropic.AnalyticsKey
			if key == "" && githubOrg == "" {
				return fmt.Errorf("%w; or pass --github-org for the organisation's Copilot budgets", orgcaps.ErrNoKey)
			}
			members, err := a.client.Members(ctx)
			if err != nil {
				return err
			}
			var reps []orgcaps.Report
			if key != "" {
				rep, err := orgcaps.ReadClaude(ctx, &anthropicapi.AnalyticsClient{Key: key}, members)
				if err != nil {
					return err
				}
				reps = append(reps, rep)
			}
			if githubOrg != "" {
				token, err := githubToken(ctx, useGH)
				if err != nil {
					return err
				}
				rep, err := orgcaps.ReadCopilot(ctx, &copilotapi.BudgetClient{Token: token}, githubOrg, members)
				if err != nil {
					return err
				}
				reps = append(reps, rep)
			}
			if af.json {
				return writeControlJSON(cmd, map[string]any{"reports": reps})
			}
			for i, rep := range reps {
				if i > 0 {
					fmt.Fprintln(cmd.OutOrStdout())
				}
				if err := writeCaps(cmd.OutOrStdout(), rep); err != nil {
					return err
				}
			}
			return nil
		}),
	}
	cmd.Flags().StringVar(&githubOrg, "github-org", "", "also read this GitHub organisation's Copilot budgets")
	cmd.Flags().BoolVar(&useGH, "gh", false, "read the GitHub token from the GitHub CLI (gh auth token) when GH_TOKEN and GITHUB_TOKEN are unset")
	return cmd
}

// githubToken is the administrator's GitHub token: GH_TOKEN, GITHUB_TOKEN,
// or with useGH the GitHub CLI's, which is another application's sign-in
// and read only on request (ADR 0013).
func githubToken(ctx context.Context, useGH bool) (string, error) {
	for _, v := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if t := strings.TrimSpace(os.Getenv(v)); t != "" {
			return t, nil
		}
	}
	if !useGH {
		return "", errors.New("no GitHub token: set GH_TOKEN or GITHUB_TOKEN, or pass --gh to use the GitHub CLI's")
	}
	t, err := copilotapi.GHSource{}.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("the GitHub CLI gave no token (`gh auth login`): %w", err)
	}
	return t, nil
}

// capsVendor names each vendor's report, what it reads and whose spend
// figure it shows.
var capsVendor = map[string]struct{ title, empty, spend string }{
	"Claude": {"Claude Enterprise spend limits", "Claude Enterprise reports no spend limits for your organisation.",
		"Spend is Anthropic's, and reads 0 while its reading is unavailable."},
	"Copilot": {"Copilot budgets", "The organisation has no budget on Copilot, its premium requests or AI credits.",
		"Spend is GitHub's this month; a budget that only alerts does not stop usage."},
}

func writeCaps(out io.Writer, rep orgcaps.Report) error {
	v := capsVendor[rep.Vendor]
	fmt.Fprintln(out, v.title)
	if len(rep.Rows) == 0 {
		fmt.Fprintln(out, "  "+v.empty)
		return nil
	}
	fmt.Fprintln(out)
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "MEMBER\tPERIOD\tCAP\tSPENT\tUSED\tFROM\tREQUEST")
	for _, r := range rep.Rows {
		limit, used := "none", "—"
		if r.Limit != nil {
			limit = inCurrency(*r.Limit, r.Currency)
			if *r.Limit == 0 {
				limit = "included only"
			}
		}
		if r.UsedPct != nil {
			used = fmt.Sprintf("%.0f%%", *r.UsedPct)
			if r.Near() {
				used += " !"
			}
		}
		request := ""
		if r.Pending {
			request = "waiting since " + r.PendingSince.Local().Format("Jan 2 15:04")
		}
		name := r.Member
		if r.MemberID == "" && !r.Shared {
			name += " (not in the team)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, r.Period, limit, inCurrency(r.Spent, r.Currency), used, r.Source, request)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	var notes []string
	if rep.Pending > 0 {
		notes = append(notes, fmt.Sprintf("%d request(s) for a higher limit wait for you in claude.ai > Organization settings", rep.Pending))
	}
	if near := countNear(rep); near > 0 {
		notes = append(notes, fmt.Sprintf("%d at %d%% of their cap or more (!)", near, orgcaps.NearPct))
	}
	if rep.NotInTeam > 0 {
		notes = append(notes, fmt.Sprintf("%d %s member(s) are not in the team", rep.NotInTeam, rep.Vendor))
	}
	if len(rep.NoSeat) > 0 {
		notes = append(notes, "no "+rep.Vendor+" limit for "+strings.Join(rep.NoSeat, ", ")+" (another address, or no seat)")
	}
	fmt.Fprintln(out)
	for _, n := range notes {
		fmt.Fprintln(out, "  "+n+".")
	}
	fmt.Fprintln(out, "  "+v.spend)
	return nil
}

func countNear(rep orgcaps.Report) int {
	n := 0
	for _, r := range rep.Rows {
		if r.Near() {
			n++
		}
	}
	return n
}

// inCurrency renders an amount in its currency: $12.50, or "12.50 EUR".
func inCurrency(v float64, currency string) string {
	if currency == "" || strings.EqualFold(currency, "USD") {
		return usd(v)
	}
	return fmt.Sprintf("%.2f %s", v, strings.ToUpper(currency))
}
