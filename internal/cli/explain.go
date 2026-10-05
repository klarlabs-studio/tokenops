package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/glossary"
)

func newExplainCmd() *cobra.Command {
	var (
		jsonOut bool
		dbPath  string
	)
	cmd := &cobra.Command{
		Use:   "explain [term | decision-id]",
		Short: "What a figure means, how it is measured, and how to read it",
		Long: `explain says in plain words what a figure means: wall-clock, turns,
rework, api-equivalent, signal quality and the rest. With no term it lists
them all.

  tokenops explain wall-clock
  tokenops explain "first-try rate"

Given a decision's ID instead — a routing or optimization decision
TokenOps recorded — it shows the evidence, the alternatives it weighed, the
policy that applied and the result, as JSON.

  tokenops explain decision:6f1c…`,
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
