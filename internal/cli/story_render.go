package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
)

// The structure is one story.Task. The renderings are what differ,
// because the audiences do: four people want an account of the same work
// and none of them wants the same document.
//
//	the operator      — what happened, candidly (writeStoryText)
//	an agent          — the same facts, enumerated (writeStoryJSON)
//	someone you bill  — evidence, readable by someone who was not there,
//	                    optimised for defensibility over candour
//	a teammate        — state of the world, optimised for what to pick up
//	                    rather than for narrative
//
// Two things stay true in every rendering. Titles are the operator's own
// words, quoted rather than paraphrased — a summariser can be wrong and a
// quote cannot. And nothing claims to know whether the work is correct:
// transcripts record what was attempted, and only the tests know the rest.

// writeStoryReport renders the account for someone you bill or report to.
//
// It is evidence, not a confession. Someone who was not there needs to
// see what the work involved — when, for how long, against which files,
// at what volume — and each of those is a fact the transcript actually
// carries. So the friction narrative is absent: "you told it the third
// answer was wrong" is candour aimed at the operator, and putting it in
// front of a client turns an account of work into an apology for it.
//
// What is absent is not hidden. The footer says plainly what the figures
// are and, more importantly, what they are not — wall-clock from local
// transcripts, which is not the same thing as a timesheet and is not a
// judgement about what is billable. A number a reader can misread as a
// stronger claim than it is is the one way this rendering can do harm.
func writeStoryReport(w io.Writer, tasks []story.Task, window string) {
	if len(tasks) == 0 {
		fmt.Fprintf(w, "No AI-assisted work recorded in %s.\n", window)
		return
	}
	byDay := groupByDay(tasks)

	fmt.Fprintf(w, "Record of AI-assisted work — %s\n", window)
	fmt.Fprintf(w, "Prepared %s from local agent transcripts.\n\n", time.Now().Local().Format("2 Jan 2006, 15:04"))

	var total time.Duration
	for _, day := range byDay {
		fmt.Fprintf(w, "%s\n", day.date.Format("Monday, 2 January 2006"))
		var dayTotal time.Duration
		for _, t := range day.tasks {
			dayTotal += t.Duration()
			fmt.Fprintf(w, "  %s–%s  %-6s %s\n",
				t.Start.Local().Format("15:04"), t.End.Local().Format("15:04"),
				humanDuration(t.Duration()), t.Title)
			fmt.Fprintf(w, "  %-20s%d instruction%s, %d turns, %d tool calls\n",
				"", t.Instructions(), plural(t.Instructions()), t.Turns(), t.ToolCalls())
			if files := t.Files(); len(files) > 0 {
				fmt.Fprintf(w, "  %-20sfiles: %s\n", "", joinTrunc(displayPaths(files), 6))
			}
		}
		total += dayTotal
		fmt.Fprintf(w, "  %-20s— %s across %d task%s\n\n",
			"", humanDuration(dayTotal), len(day.tasks), plural(len(day.tasks)))
	}

	fmt.Fprintf(w, "Total: %d task%s over %d day%s, %s of session time.\n",
		len(tasks), plural(len(tasks)), len(byDay), plural(len(byDay)), humanDuration(total))
	fmt.Fprint(w, "\nFigures are wall-clock from the first instruction of a task to the\n"+
		"agent's last turn on it, reconstructed from transcripts on this machine.\n"+
		"That is a record of when work happened, not a timesheet and not a\n"+
		"judgement about what is billable. Titles are the instructions as they\n"+
		"were typed. Nothing here asserts that the work is correct.\n")
}

// writeStoryHandoff renders the account for a teammate picking up the work.
//
// They want the state of the world, not the story of how it got there, so
// the ordering is by what needs attention rather than by time: work that
// ended on friction first, because that is where the loose ends are, then
// what ran clean, then the files to open.
//
// It never says a task "landed". A transcript records what was attempted
// and how it ended; whether the code works is a question only the tests
// answer, and a handover that quietly implies otherwise is worse than no
// handover at all.
func writeStoryHandoff(w io.Writer, tasks []story.Task, window string) {
	if len(tasks) == 0 {
		fmt.Fprintf(w, "No work recorded in %s — nothing to hand over.\n", window)
		return
	}
	var rough, clean []story.Task
	for _, t := range tasks {
		if t.Clean() {
			clean = append(clean, t)
		} else {
			rough = append(rough, t)
		}
	}

	fmt.Fprintf(w, "Handover — %s, %d task%s\n\n", window, len(tasks), plural(len(tasks)))

	if len(rough) > 0 {
		fmt.Fprintln(w, "Ended on friction — start here")
		for _, t := range rough {
			writeHandoffTask(w, t)
			for _, f := range t.Frictions() {
				fmt.Fprintf(w, "      %s: %s\n", f.Kind, f.Detail)
			}
			fmt.Fprintln(w)
		}
	}
	if len(clean) > 0 {
		fmt.Fprintln(w, "Ran clean — nothing rejected, reworked or interrupted")
		for _, t := range clean {
			writeHandoffTask(w, t)
			fmt.Fprintln(w)
		}
	}

	if hot := hotFiles(tasks); len(hot) > 0 {
		fmt.Fprintln(w, "Files this work touched most")
		for _, f := range hot {
			fmt.Fprintf(w, "  %-52s %d task%s\n", displayPath(f.name), f.n, plural(f.n))
		}
		fmt.Fprintln(w)
	}

	fmt.Fprint(w, "Reconstructed from local transcripts. It says what was asked for and\n"+
		"how the work ended, not whether it works — only the tests know that.\n")
}

func writeHandoffTask(w io.Writer, t story.Task) {
	fmt.Fprintf(w, "  ─ %s\n", t.Title)
	fmt.Fprintf(w, "      %s, %s, %d instruction%s\n",
		t.Start.Local().Format("Mon 2 Jan 15:04"), humanDuration(t.Duration()),
		t.Instructions(), plural(t.Instructions()))
	if files := t.Files(); len(files) > 0 {
		fmt.Fprintf(w, "      touched %s\n", joinTrunc(displayPaths(files), 6))
	}
}

// --- shared shaping --------------------------------------------------------

type dayGroup struct {
	date  time.Time
	tasks []story.Task
}

// groupByDay buckets tasks by local calendar day, oldest first. A record
// of work reads forwards: a reader who was not there is following what
// happened, not catching up on what just did.
func groupByDay(tasks []story.Task) []dayGroup {
	idx := map[string]int{}
	var out []dayGroup
	ordered := append([]story.Task(nil), tasks...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Start.Before(ordered[j].Start) })
	for _, t := range ordered {
		day := t.Start.Local().Format("2006-01-02")
		i, ok := idx[day]
		if !ok {
			d := t.Start.Local()
			out = append(out, dayGroup{date: time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, d.Location())})
			i = len(out) - 1
			idx[day] = i
		}
		out[i].tasks = append(out[i].tasks, t)
	}
	return out
}

type fileCount struct {
	name string
	n    int
}

// hotFiles ranks the files the work kept coming back to. Ties break on
// name so the same window always renders the same way.
func hotFiles(tasks []story.Task) []fileCount {
	n := map[string]int{}
	for _, t := range tasks {
		for _, f := range t.Files() {
			n[f]++
		}
	}
	out := make([]fileCount, 0, len(n))
	for name, c := range n {
		out = append(out, fileCount{name: name, n: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].name < out[j].name
	})
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

// displayPath writes a file path for someone who is not sitting at this
// machine. The home directory becomes "~" — a client does not need the
// operator's username, and a teammate does not need a path that only
// resolves on one laptop — and a long path is elided from the left, since
// the end of a path is the part that identifies the file.
func displayPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			p = "~/" + filepath.ToSlash(rel)
		}
	}
	const max = 52
	r := []rune(p)
	if len(r) <= max {
		return p
	}
	tail := string(r[len(r)-(max-1):])
	if i := strings.IndexByte(tail, '/'); i >= 0 {
		tail = tail[i:]
	}
	return "…" + tail
}

func displayPaths(ps []string) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, displayPath(p))
	}
	return out
}
