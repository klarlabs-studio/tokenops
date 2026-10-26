package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/prefixlint"
)

// newCoachLintContextCmd lints the always-loaded context for text that
// changes between sessions or turns and so breaks the prompt cache.
func newCoachLintContextCmd() *cobra.Command {
	var (
		root        string
		transcripts string
		max         int
		jsonOut     bool
	)
	cmd := &cobra.Command{
		Use:   "lint-context",
		Short: "Find what in every session's context changes and breaks the prompt cache",
		Long: `lint-context reads what your agents load into every session and reports
what changes between sessions or turns: a date, a timestamp, a UUID, a
counter, a SessionStart hook that runs date or git status. A provider
caches a prompt up to the first byte that differs from an earlier one, so
a timestamp near the start makes every session rebuild the cache from
there.

It reads Claude Code's CLAUDE.md files (yours, the project's and its
parents' up to the repository root, and the files they @import, one level),
the SessionStart and UserPromptSubmit hooks in its settings, and the
AGENTS.md files Codex and opencode load. Hooks are never run: their
command text is read, and the output the newest transcripts recorded for
them. Each finding carries the matched token only, never the text around
it, with why it breaks the cache and the fix. Nothing is sent anywhere.

Severity: high is in every session's prefix and differs each time (a
SessionStart hook printing the time); medium changes often (a per-turn
hook's output, a "last updated" line a tool rewrites); low changes only
when someone edits the file (a date written in CLAUDE.md).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if root == "" {
				wd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("resolve working directory: %w", err)
				}
				root = wd
			}
			src := prefixlint.ProjectSources(root, transcripts)
			if max > 0 {
				src.MaxTranscripts = max
			}
			r := prefixlint.Lint(src)
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(r)
			}
			renderLintContext(cmd, r)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "project directory (default: the working directory)")
	cmd.Flags().StringVar(&transcripts, "transcripts", "", "Claude Code projects directory to read hook output from (default ~/.claude/projects; - skips it)")
	cmd.Flags().IntVar(&max, "max-transcripts", 0, fmt.Sprintf("newest transcripts to read (default %d)", prefixlint.DefaultMaxTranscripts))
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

func renderLintContext(cmd *cobra.Command, r prefixlint.Report) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Volatile context: %d findings (%d high, %d medium, %d low)\n", len(r.Findings), r.High, r.Medium, r.Low)
	fmt.Fprintf(out, "  read %d files", len(r.Read))
	if r.Transcripts > 0 {
		fmt.Fprintf(out, " and %d transcripts", r.Transcripts)
	}
	fmt.Fprintln(out)
	if len(r.Findings) == 0 {
		fmt.Fprintln(out, "  nothing in the always-loaded context changes between sessions")
		return
	}
	for _, sev := range []string{prefixlint.SeverityHigh, prefixlint.SeverityMedium, prefixlint.SeverityLow} {
		for _, f := range r.Findings {
			if f.Severity != sev {
				continue
			}
			fmt.Fprintln(out)
			fmt.Fprintf(out, "%-6s %s\n", strings.ToUpper(f.Severity), lintWhere(f))
			fmt.Fprintf(out, "       %s\n", lintWhat(f))
			fmt.Fprintf(out, "       why: %s\n", f.Why)
			fmt.Fprintf(out, "       fix: %s\n", f.Fix)
		}
	}
}

func lintWhere(f prefixlint.Finding) string {
	where := f.Path
	if where == "" {
		where = "(hook no longer configured)"
	} else if f.Line > 0 {
		where = fmt.Sprintf("%s:%d", f.Path, f.Line)
	}
	if f.Hook != "" {
		where += "  " + f.Hook + " hook"
	}
	return where
}

func lintWhat(f prefixlint.Finding) string {
	switch f.Source {
	case prefixlint.SourceHook:
		return fmt.Sprintf("runs `%s` (%s)", f.Token, f.Kind)
	case prefixlint.SourceHookOutput:
		return fmt.Sprintf("`%s` printed %s: %s", f.Command, f.Kind, f.Token)
	}
	return fmt.Sprintf("%s: %s", f.Kind, f.Token)
}
