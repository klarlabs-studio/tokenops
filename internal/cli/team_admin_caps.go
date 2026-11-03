package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/orgcaps"
	anthropicapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/anthropic"
)

// The vendor's caps on each member, read with the administrator's own
// vendor key on this machine (gap G7).

func newTeamAdminCapsCmd(rf *rootFlags, af *adminFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "caps",
		Short: "Each member's Claude Enterprise spend limit, spend and pending requests for more, beside the team",
		Long: `caps reads the spend limit Claude Enterprise sets on each member: the cap,
where it was inherited from (set for them, their seat tier, a group or the
organisation), the spend so far in its period, and any request for a higher
limit waiting for an admin. It is the ceiling your organisation's boundaries
sit under: a boundary can ask or refuse before a member reaches the cap, not
lift it.

It reads with vendor_usage.anthropic.analytics_key
(TOKENOPS_ANTHROPIC_ANALYTICS_KEY), a key the primary owner creates in
claude.ai > Organization settings > API with the read:spend_limits scope
(one key can carry read:analytics beside it). The key stays on this machine.
Members are matched to the team by e-mail address in memory; nothing is
uploaded or kept.

Requests are approved or denied in claude.ai, where the new limit is set.
OpenAI's per-seat credit limits are set only in its billing settings, with no
API to read them.`,
		Args: cobra.NoArgs,
		RunE: adminRun(rf, af, func(ctx context.Context, cmd *cobra.Command, a adminCtx, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			key := cfg.VendorUsage.Anthropic.AnalyticsKey
			if key == "" {
				return orgcaps.ErrNoKey
			}
			members, err := a.client.Members(ctx)
			if err != nil {
				return err
			}
			rep, err := orgcaps.Read(ctx, &anthropicapi.AnalyticsClient{Key: key}, members)
			if err != nil {
				return err
			}
			if af.json {
				return writeControlJSON(cmd, rep)
			}
			return writeCaps(cmd.OutOrStdout(), rep)
		}),
	}
}

func writeCaps(out io.Writer, rep orgcaps.Report) error {
	if len(rep.Rows) == 0 {
		fmt.Fprintln(out, "Claude Enterprise reports no spend limits for your organisation.")
		return nil
	}
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
		if r.MemberID == "" {
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
		notes = append(notes, fmt.Sprintf("%d Claude member(s) are not in the team", rep.NotInTeam))
	}
	if len(rep.NoSeat) > 0 {
		notes = append(notes, "no Claude Enterprise limit for "+strings.Join(rep.NoSeat, ", ")+" (another address, or no seat)")
	}
	if len(notes) > 0 {
		fmt.Fprintln(out)
		for _, n := range notes {
			fmt.Fprintln(out, "  "+n+".")
		}
	}
	fmt.Fprintln(out, "\n  Spend is Anthropic's, and reads 0 while its reading is unavailable.")
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
