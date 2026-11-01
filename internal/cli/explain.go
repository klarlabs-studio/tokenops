package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"time"

	"github.com/spf13/cobra"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/glossary"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/routeguard"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func newExplainCmd() *cobra.Command {
	var (
		jsonOut bool
		dbPath  string
	)
	cmd := &cobra.Command{
		Use:   "explain [term | decision-id | moves | price]",
		Short: "What a figure means, how it is measured, and how to read it",
		Long: `explain says in plain words what a figure means: wall-clock, turns,
rework, api-equivalent, signal quality and the rest. With no term it lists
them all.

  tokenops explain wall-clock
  tokenops explain "first-try rate"

Given a decision's ID instead — a routing or optimization decision
TokenOps recorded — it shows the evidence, the alternatives it weighed, the
policy that applied and the result, as JSON.

  tokenops explain decision:6f1c…

"moves" shows, per kind of model move the coach made on its own, how
many it made, how many outcomes were judged against comparable unmoved
work, the verdict and why, and any move it retracted because quality
dropped.

  tokenops explain moves

"price" shows the price of quota on the Claude plan now (λ, 0 to 1): the
binding window it comes from, the share the rest of that window wants at
the pace so far against the share left, and for edits and deep work
whether the tier they fit is worth its extra plan use at that price or
they go a tier down.

  tokenops explain price`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				if jsonOut {
					return json.NewEncoder(out).Encode(glossary.List())
				}
				writeExplainList(out)
				return nil
			}
			if strings.EqualFold(strings.TrimSpace(args[0]), "moves") {
				v := coachcap.Verdicts(coachcap.MoveStore(), time.Now())
				if jsonOut {
					return json.NewEncoder(out).Encode(v)
				}
				writeMoveVerdicts(out, v)
				return nil
			}
			if strings.EqualFold(strings.TrimSpace(args[0]), "price") {
				v := explainQuotaPrice(time.Now())
				if jsonOut {
					return json.NewEncoder(out).Encode(v)
				}
				writeQuotaPrice(out, v)
				return nil
			}
			t, ok, suggestions := glossary.Lookup(args[0])
			if !ok {
				// Not a term, so perhaps a decision. A store that cannot
				// be read matters only when the argument is plainly a
				// decision's ID; a mistyped term deserves suggestions.
				found, err := explainDecision(cmd, dbPath, args[0])
				if found || (err != nil && strings.HasPrefix(args[0], "decision:")) {
					return err
				}
				if len(suggestions) > 0 {
					return fmt.Errorf("no term or decision %q; did you mean: %s", args[0], strings.Join(suggestions, ", "))
				}
				return fmt.Errorf("no term or decision %q; `tokenops explain` lists the terms", args[0])
			}
			if jsonOut {
				return json.NewEncoder(out).Encode(t)
			}
			writeExplainTerm(out, t)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	cmd.Flags().StringVar(&dbPath, "db", "", "event store for decisions (defaults to the configured one)")
	return cmd
}

// writeExplainList prints every term by area.
func writeExplainList(w io.Writer) {
	area := ""
	for _, t := range glossary.List() {
		if t.Area != area {
			area = t.Area
			fmt.Fprintf(w, "\n%s\n", strings.ToUpper(area))
		}
		fmt.Fprintf(w, "  %-16s %s\n", t.Name, t.Short)
	}
	fmt.Fprintln(w, "\nMore on one: tokenops explain <term>")
}

// writeExplainTerm prints one term.
func writeExplainTerm(w io.Writer, t glossary.Term) {
	fmt.Fprintf(w, "%s — %s\n\n", t.Name, t.Short)
	fmt.Fprintf(w, "What it measures\n  %s\n\n", t.What)
	fmt.Fprintf(w, "How it is worked out\n  %s\n\n", t.How)
	fmt.Fprintf(w, "How to read it\n  %s\n", t.Read)
	if t.Grades != "" {
		fmt.Fprintf(w, "\nGrades\n  %s\n", t.Grades)
	}
	fmt.Fprintf(w, "\nShown by: %s\n", t.Where)
}

// writeMoveVerdicts prints what the evidence says about each kind of
// autonomous model move.
func writeMoveVerdicts(w io.Writer, v coachcap.MoveVerdicts) {
	if len(v.Lines) == 0 {
		fmt.Fprintln(w, "No autonomous model moves to judge yet. Each subagent the coach moves to a cheaper model, and each session")
		fmt.Fprintln(w, "started on a lowered default, is compared with like work that was not moved once enough of both exist.")
		return
	}
	fmt.Fprintln(w, "Autonomous model moves, judged against comparable unmoved work (same kind, same project, within 7 days):")
	fmt.Fprintln(w)
	for _, l := range v.Lines {
		fmt.Fprintf(w, "  %s\n", l)
	}
	fmt.Fprintln(w, "\nA bad outcome is a subagent that failed, was re-run, had its files reworked, or whose turn was interrupted or")
	fmt.Fprintln(w, "rejected; for new sessions, an instruction reworked, interrupted or rejected (first 20 per session).")
}

// quotaPriceView is `tokenops explain price`: the price of quota on the
// Claude plan and what it does to routing now.
type quotaPriceView struct {
	// Price is nil when no fresh reading of the plan is on the machine.
	Price       *routeguard.QuotaPrice  `json:"price"`
	Escalations []routeguard.Escalation `json:"escalations"`
}

// explainQuotaPrice reads the price as the routing hooks do, and weighs
// each kind of work it can move on the configured models.
func explainQuotaPrice(now time.Time) quotaPriceView {
	v := quotaPriceView{Price: routeguard.ReadQuotaPrice("", boundaryledger.DefaultDir(), now), Escalations: []routeguard.Escalation{}}
	if v.Price == nil {
		return v
	}
	cfg := config.Default()
	if path, err := config.DefaultPath(); err == nil {
		if loaded, err := config.Load(path); err == nil {
			cfg = loaded
		}
	}
	if e := routeguard.Escalations(v.Price, routeCatalog(cfg), eventschema.ProviderAnthropic, cfg.RoutingCandidates(eventschema.ProviderAnthropic)); e != nil {
		v.Escalations = e
	}
	return v
}

// writeQuotaPrice prints the price of quota and what it does to routing.
func writeQuotaPrice(w io.Writer, v quotaPriceView) {
	p := v.Price
	if p == nil {
		fmt.Fprintln(w, "No price of quota: no fresh reading of the Claude plan's windows (the status line's, or the daemon's figures).")
		fmt.Fprintln(w, "Routing places work by what it is alone.")
		return
	}
	fmt.Fprintf(w, "Price of quota (λ): %s, from the %s %s window (binding: the highest price of the plan's windows)\n",
		routeguard.FormatQuotaPrice(p.Lambda), p.Vendor, p.Window)
	switch p.Basis {
	case routeguard.PriceBasisPace:
		fmt.Fprintf(w, "  At the pace so far the rest of the window wants ~%.0f%% of the plan; %.0f%% is left.\n", p.WantedPct, p.LeftPct)
		fmt.Fprintln(w, "  λ = 1 − left ÷ wanted: 0 while the window lasts to its reset, nearer 1 the longer it would stand empty.")
	case routeguard.PriceBasisShareLeft:
		fmt.Fprintf(w, "  %.0f%% is left and the pace cannot be judged yet (too early in the window, or no reset known).\n", p.LeftPct)
		fmt.Fprintln(w, "  λ = 1 − left ÷ 33%: 0 at a third left or more, 1 at none.")
	case routeguard.PriceBasisUsedUp:
		fmt.Fprintln(w, "  The window is used up: λ is at its bound, 1.")
	}
	if !p.ResetsAt.IsZero() {
		fmt.Fprintf(w, "  Resets %s.", p.ResetsAt.Local().Format("Mon 15:04"))
		fmt.Fprintln(w)
	}
	if p.Source != "" {
		fmt.Fprintf(w, "  Source: %s, observed %s.\n", p.Source, p.ObservedAt.Local().Format("15:04"))
	}
	fmt.Fprintln(w)
	if len(v.Escalations) == 0 {
		fmt.Fprintln(w, "No kind of work has a tier below it on the configured models, so the price moves nothing.")
	} else {
		fmt.Fprintln(w, "A tier up is taken only when its quality gain exceeds λ × its extra plan use:")
		for _, e := range v.Escalations {
			fmt.Fprintf(w, "  %s\n", e.Why())
		}
	}
	fmt.Fprintln(w, "\nThe headroom note to the agent stays information, never a cap. ADR 0017 has the formula and its defaults.")
}
