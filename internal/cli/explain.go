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
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "explain [term]",
		Short: "Explain a figure TokenOps shows: what it measures, how, and how to read it",
		Long: `explain says in plain words what a figure means: wall-clock, turns,
rework, api-equivalent, signal quality and the rest. With no term it lists
them all.

  tokenops explain wall-clock
  tokenops explain "first-try rate"`,
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
				if len(suggestions) > 0 {
					return fmt.Errorf("no term %q; did you mean: %s", args[0], strings.Join(suggestions, ", "))
				}
				return fmt.Errorf("no term %q; `tokenops explain` lists them all", args[0])
			}
			if jsonOut {
				return json.NewEncoder(out).Encode(t)
			}
			writeExplainTerm(out, t)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
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
