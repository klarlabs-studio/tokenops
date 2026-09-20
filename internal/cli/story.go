package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/reconstruct"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
)

// newStoryCmd accounts for what actually happened in a piece of work.
//
// dx answers "what are my sessions like" in aggregate; this answers "what
// happened in this one" — what you asked for, what the agent did about
// it, what it cost, and where it went sideways. The aggregate tells you
// there is a problem; the account tells you what the problem was.
func newStoryCmd() *cobra.Command {
	var (
		root     string
		source   string
		days     int
		limit    int
		idleGap  time.Duration
		jsonOut  bool
		audience string

		includeScratch bool
	)
	cmd := &cobra.Command{
		Use:   "story",
		Short: "What you asked for, what the agent did, and where it went sideways",
		Long: `story reconstructs your work as an account of it, one task at a time:
the instruction you typed, everything the agent did before the next one,
what it cost, and the specific moments it went wrong — an answer you
rejected, a file edited twice, a turn you had to stop.

A task is a run of consecutive instructions on one piece of work.
Boundaries are inferred, not demanded: a new session always starts a new
task, and a pause longer than --idle-gap splits one. Every task says which
of those split it, because a boundary you can see is one you can argue
with. ` + "`tokenops task start|done`" + ` remains the way to mark one exactly.

The account is one structure; --for chooses who it is written for,
because four people want the same work described and none of them wants
the same document:

  me        what happened, candidly — the default
  agent     the same facts enumerated as JSON, for a machine reading it
            back (--json is the same thing)
  report    evidence for someone you bill or report to, readable by
            someone who was not there
  handoff   the state of the world for a teammate picking the work up

Titles are your own instructions, quoted rather than paraphrased: a
summariser can be wrong and a quote cannot. No rendering claims the work
is correct — transcripts record what was attempted, and only the tests
know the rest.

Prompt text is read at scan time and never persisted. Nothing here is
stored; the account is rebuilt from transcripts on every run.`,
		Args: cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			return validateAudience(audience)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := agentdx.ExtractOptions{
				Root:   root,
				Source: agentdx.Source(source),
				// The narrative needs the words. This is the only surface
				// that asks for them.
				WithPromptText: true,
				IncludeScratch: includeScratch,
			}
			if days > 0 {
				opts.Since = time.Now().AddDate(0, 0, -days)
			}
			records, err := agentdx.ExtractAll(opts)
			if err != nil {
				// A reader that broke is reported, but whatever the other
				// clients yielded is still worth showing.
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
			}
			tasks := story.Group(agentdx.Units(records), story.Options{IdleGap: idleGap})
			// Newest first: the work you are most likely asking about is
			// the work you just did.
			reverse(tasks)
			if limit > 0 && len(tasks) > limit {
				tasks = tasks[:limit]
			}
			out := cmd.OutOrStdout()
			switch resolveAudience(audience, jsonOut) {
			case audienceAgent:
				return writeStoryJSON(out, tasks, days)
			case audienceReport:
				writeStoryReport(out, tasks, storyWindow(days))
			case audienceHandoff:
				writeStoryHandoff(out, tasks, storyWindow(days))
			default:
				writeStoryText(out, tasks, days)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "transcript root (defaults per source)")
	cmd.Flags().StringVar(&source, "source", "auto", "client: auto | claude-code | codex | cursor | opencode")
	cmd.Flags().IntVar(&days, "days", 7, "window in days; 0 reads everything")
	cmd.Flags().IntVar(&limit, "limit", 10, "show at most this many tasks; 0 for all")
	cmd.Flags().DurationVar(&idleGap, "idle-gap", 0, "pause that starts a new task (default 10m; negative disables splitting)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON (the same as --for agent)")
	cmd.Flags().StringVar(&audience, "for", audienceMe, "who the account is written for: me | agent | report | handoff")
	cmd.Flags().BoolVar(&includeScratch, "include-scratch", false, scratchFlagHelp)
	return cmd
}

// Audiences for `story --for`. The account is one structure; these choose
// the rendering.
const (
	audienceMe      = "me"
	audienceAgent   = "agent"
	audienceReport  = "report"
	audienceHandoff = "handoff"
)

// validateAudience rejects an unknown audience rather than quietly
// falling back to the default. A typo that silently produces the
// operator's own rendering is how someone emails a client the candid one.
func validateAudience(a string) error {
	switch strings.ToLower(strings.TrimSpace(a)) {
	case "", audienceMe, audienceAgent, audienceReport, audienceHandoff:
		return nil
	default:
		return fmt.Errorf("--for %q: want one of %s, %s, %s, %s",
			a, audienceMe, audienceAgent, audienceReport, audienceHandoff)
	}
}

// resolveAudience applies --json, which predates --for and keeps working
// as the machine rendering it always was.
func resolveAudience(a string, jsonOut bool) string {
	if jsonOut {
		return audienceAgent
	}
	a = strings.ToLower(strings.TrimSpace(a))
	if a == "" {
		return audienceMe
	}
	return a
}

// storyWindow names the window in the words a reader outside the terminal
// would use. "last 7d" is shell shorthand; a report goes to someone who
// has never seen this tool.
func storyWindow(days int) string {
	switch {
	case days <= 0:
		return "all recorded history"
	case days == 1:
		return "the last day"
	case days == 7:
		return "the last week"
	case days%7 == 0:
		return fmt.Sprintf("the last %d weeks", days/7)
	default:
		return fmt.Sprintf("the last %d days", days)
	}
}

func reverse(ts []story.Task) {
	for i, j := 0, len(ts)-1; i < j; i, j = i+1, j-1 {
		ts[i], ts[j] = ts[j], ts[i]
	}
}

// storyTaskJSON is the machine rendering. It is the same account, shaped
// for an agent reading its own history back — which is why the frictions
// are enumerated rather than prose-formatted.
type storyTaskJSON struct {
	Title        string  `json:"title"`
	SessionID    string  `json:"session_id"`
	Provider     string  `json:"provider,omitempty"`
	Start        string  `json:"start"`
	End          string  `json:"end"`
	DurationSec  float64 `json:"duration_seconds"`
	Boundary     string  `json:"boundary"`
	Instructions int     `json:"instructions"`
	Turns        int     `json:"turns"`
	ToolCalls    int     `json:"tool_calls"`
	// ContextCarriedTokens double-counts: every turn re-sends the
	// accumulated context. It is here for comparing tasks, never as a
	// spend figure. PeakContextTokens counts the context once.
	ContextCarriedTokens int64    `json:"context_carried_tokens"`
	PeakContextTokens    int64    `json:"peak_context_tokens"`
	Files                []string `json:"files,omitempty"`
	Frictions            []string `json:"frictions"`
	Clean                bool     `json:"clean"`
}

func writeStoryJSON(w io.Writer, tasks []story.Task, days int) error {
	out := struct {
		WindowDays int             `json:"window_days"`
		Tasks      []storyTaskJSON `json:"tasks"`
		// Work is the same reconstruction seen through the work
		// ontology: which goal, which attempt at it, and whether
		// anything judged the result.
		//
		// An agent reading this used to get a list of tasks with no way
		// to tell those three apart — and in particular no way to know
		// that a story's title is a heuristic's guess rather than
		// something a person wrote, or that no outcome means nobody
		// assessed the work rather than that it failed.
		//
		// Added beside the existing fields, which are unchanged, so
		// anything already parsing this keeps working.
		Work []reconstruct.Work `json:"work"`
	}{
		WindowDays: days,
		Tasks:      make([]storyTaskJSON, 0, len(tasks)),
		Work:       make([]reconstruct.Work, 0, len(tasks)),
	}
	out.Work = append(out.Work, reconstruct.FromTasks(tasks)...)
	for _, t := range tasks {
		frictions := []string{}
		for _, f := range t.Frictions() {
			frictions = append(frictions, f.Detail)
		}
		out.Tasks = append(out.Tasks, storyTaskJSON{
			Title:                t.Title,
			SessionID:            t.SessionID,
			Provider:             t.Provider,
			Start:                t.Start.Format(time.RFC3339),
			End:                  t.End.Format(time.RFC3339),
			DurationSec:          t.Duration().Seconds(),
			Boundary:             string(t.Boundary),
			Instructions:         t.Instructions(),
			Turns:                t.Turns(),
			ToolCalls:            t.ToolCalls(),
			ContextCarriedTokens: t.ContextCarried(),
			PeakContextTokens:    t.PeakContext(),
			Files:                t.Files(),
			Frictions:            frictions,
			Clean:                t.Clean(),
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func writeStoryText(w io.Writer, tasks []story.Task, days int) {
	if len(tasks) == 0 {
		fmt.Fprintf(w, "No tasks in the last %dd. Run an agent session, or widen --days.\n", days)
		return
	}
	window := fmt.Sprintf("last %dd", days)
	if days == 0 {
		window = "all history"
	}
	fmt.Fprintf(w, "Your work — %s\n  %d task%s\n\n", window, len(tasks), plural(len(tasks)))

	for _, t := range tasks {
		fmt.Fprintf(w, "─ %s\n", t.Title)
		fmt.Fprintf(w, "  %s → %s (%s)",
			t.Start.Local().Format("Mon 15:04"), t.End.Local().Format("15:04"), humanDuration(t.Duration()))
		if t.Boundary != story.BoundarySessionStart {
			fmt.Fprintf(w, "  · split by %s", t.Boundary)
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  %d instruction%s · %d turns · %d tool calls · context peaked at %s\n",
			t.Instructions(), plural(t.Instructions()), t.Turns(), t.ToolCalls(), humanTokens(t.PeakContext()))
		if files := t.Files(); len(files) > 0 {
			fmt.Fprintf(w, "  touched %s\n", joinTrunc(files, 4))
		}
		if fr := t.Frictions(); len(fr) > 0 {
			fmt.Fprintln(w, "  where it went sideways:")
			for _, f := range fr {
				fmt.Fprintf(w, "    · %s\n", f.Detail)
			}
		} else {
			fmt.Fprintln(w, "  clean run — no rework, nothing rejected, nothing interrupted")
		}
		fmt.Fprintln(w)
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// joinTrunc lists names, naming the overflow rather than hiding it.
func joinTrunc(vs []string, max int) string {
	if len(vs) <= max {
		return strings.Join(vs, ", ")
	}
	return strings.Join(vs[:max], ", ") + fmt.Sprintf(" and %d more", len(vs)-max)
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
