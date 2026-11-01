package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// finOpsNote says whether the FinOps sections of a preview are sent.
func finOpsNote(joined bool, sentSchema int) string {
	switch {
	case !joined:
		return "Sent only to a team server that accepts upload schema 2:"
	case sentSchema < teamwire.SchemaFinOps:
		return "Withheld: the team server reads upload schema 1 only and does not accept these yet. " +
			"They are sent once it does (checked daily):"
	}
	return "Sent with the buckets (upload schema 2):"
}

// writeFinOps prints the schema 2 sections of u under note.
func writeFinOps(out io.Writer, u teamwire.Upload, note string) {
	if len(u.PlanDays)+len(u.Waste)+len(u.Savings)+len(u.PlanAdvice) == 0 {
		fmt.Fprintln(out, "\nNo plan use, waste, savings or plan advice measured in these days.")
		return
	}
	fmt.Fprintln(out, "\n"+note)
	if len(u.PlanDays) > 0 {
		fmt.Fprintf(out, "\nPlan use: %d rows.\n", len(u.PlanDays))
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', tabwriter.AlignRight)
		fmt.Fprintln(w, "DAY\tPROVIDER\tPLAN\tUTILIZATION\tCOVERAGE\tLOCKOUTS\tBLOCKED MIN\t")
		for _, p := range u.PlanDays {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t\n", p.Day, p.Provider, p.Plan, optPct(p.UtilizationPct),
				optPct(p.CoveragePct), p.Lockouts, p.BlockedMinutes)
		}
		_ = w.Flush()
	}
	if len(u.Waste) > 0 {
		fmt.Fprintf(out, "\nAvoidable usage: %d rows.\n", len(u.Waste))
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', tabwriter.AlignRight)
		fmt.Fprintln(w, "DAY\tREPO\tKIND\tCLASS\tTURNS\tTOKENS\tVALUE\t")
		for _, x := range u.Waste {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t$%.2f\t\n", x.Day, x.Repo, x.Kind, x.Class, x.Turns, x.Tokens, x.APIEquivalentUSD)
		}
		_ = w.Flush()
	}
	if len(u.Savings) > 0 {
		fmt.Fprintf(out, "\nRealized savings: %d days.\n", len(u.Savings))
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', tabwriter.AlignRight)
		fmt.Fprintln(w, "DAY\tINTERVENTIONS\tHELD BACK\tREFUSED RE-READS\tHOLDOUT\tREALIZED\tSTILL AVOIDABLE\t")
		for _, s := range u.Savings {
			holdout := "—"
			if s.HoldoutUSD != nil {
				holdout = fmt.Sprintf("$%.2f", *s.HoldoutUSD)
			}
			fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%s\t$%.2f\t$%.2f\t\n", s.Day, s.Interventions, s.Held, s.RefusedRereads,
				holdout, s.RealizedUSD, s.AvoidableUSD)
		}
		_ = w.Flush()
	}
	if len(u.PlanAdvice) > 0 {
		fmt.Fprintf(out, "\nPlan advice: %d providers.\n", len(u.PlanAdvice))
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "PROVIDER\tPLAN\tVERDICT\tRECOMMENDED\tSAVES / MONTH\tLOCKOUTS\tCONFIDENCE\tDAYS")
		for _, a := range u.PlanAdvice {
			rec := a.RecommendedPlan
			if rec == "" {
				rec = a.Kind
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t$%.2f\t%d\t%s\t%.0f\n", a.Provider, a.Plan, a.Verdict, rec,
				a.MonthlyDeltaUSD, a.RecommendedLockouts, a.Confidence, a.Days)
		}
		_ = w.Flush()
	}
}

func optPct(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", *v)
}

// spendNote says whether the schema 3 sections of a preview are sent.
func spendNote(joined bool, sentSchema int) string {
	switch {
	case !joined:
		return "Sent only to a team server that accepts upload schema 3:"
	case sentSchema < teamwire.SchemaSpend:
		return "Withheld: the team server does not accept upload schema 3 yet. They are sent once it does (checked daily):"
	}
	return "Sent with the buckets (upload schema 3):"
}

// writeSpend prints the schema 3 sections of u under note.
func writeSpend(out io.Writer, u teamwire.Upload, note string) {
	if len(u.Spend)+len(u.Units)+len(u.VendorCosts) == 0 {
		fmt.Fprintln(out, "\nNo spend by provider or finished work measured in these days.")
		return
	}
	fmt.Fprintln(out, "\n"+note)
	if len(u.Spend) > 0 {
		fmt.Fprintf(out, "\nSpend by provider: %d rows.\n", len(u.Spend))
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', tabwriter.AlignRight)
		fmt.Fprintln(w, "DAY\tREPO\tPROVIDER\tTURNS\tTOKENS\tBILLED\tVALUE\tUNPRICED\t")
		for _, r := range u.Spend {
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t$%.2f\t$%.2f\t%d\t\n", r.Day, r.Repo, r.Provider, r.Turns, r.Tokens,
				r.CostUSD, r.APIEquivalentUSD, r.UnpricedTurns)
		}
		_ = w.Flush()
	}
	if len(u.Units) > 0 {
		fmt.Fprintf(out, "\nFinished work: %d rows.\n", len(u.Units))
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', tabwriter.AlignRight)
		fmt.Fprintln(w, "DAY\tREPO\tTASKS\tVALUE\tCOMMITS\tVALUE\tREVERTED\tVALUE\tMERGED PRS\tVALUE\t")
		for _, r := range u.Units {
			fmt.Fprintf(w, "%s\t%s\t%d\t$%.2f\t%d\t$%.2f\t%d\t$%.2f\t%d\t$%.2f\t\n", r.Day, r.Repo, r.Tasks, r.TaskUSD,
				r.Commits, r.CommitUSD, r.RevertedCommits, r.RevertedCommitUSD, r.MergedPRs, r.MergedPRUSD)
		}
		_ = w.Flush()
	}
	if len(u.VendorCosts) > 0 {
		fmt.Fprintf(out, "\nVendor cost, read with this machine's admin keys (owners and admins only): %d rows.\n", len(u.VendorCosts))
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', tabwriter.AlignRight)
		fmt.Fprintln(w, "DAY\tPROVIDER\tBILLED\t")
		for _, r := range u.VendorCosts {
			fmt.Fprintf(w, "%s\t%s\t$%.2f\t\n", r.Day, r.Provider, r.CostUSD)
		}
		_ = w.Flush()
	}
}
