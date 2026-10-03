// Package sessions answers what agent sessions are like to work with and
// what happened in them, from the transcripts the clients already write.
// The MCP tools and the daemon API both call it (ADR 0010).
package sessions

import (
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
)

// Window selects which transcripts to read.
type Window struct {
	// Root is the transcript root; empty resolves each client's default.
	Root string
	// Days is a positive day count; zero means the default of seven.
	Days int
	// All reads every transcript and overrides Days.
	All bool
}

// days resolves the window: a positive day count, or -1 for all history.
//
// Zero has to mean the default, because that is what omitting the field
// sends; "everything" gets its own switch. A negative Days still reads
// everything, as it always did.
func (w Window) days() int {
	switch {
	case w.All:
		return -1
	case w.Days == 0:
		return 7
	}
	return w.Days
}

func (w Window) options(now time.Time, withPromptText bool) (agentdx.ExtractOptions, string) {
	opts := agentdx.ExtractOptions{Root: w.Root, WithPromptText: withPromptText}
	days := w.days()
	if days <= 0 {
		return opts, "all history"
	}
	opts.Since = now.AddDate(0, 0, -days)
	return opts, "last " + strconv.Itoa(days) + "d"
}

// readWarnings turns a partial read's error into one warning per source.
func readWarnings(err error) []string {
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		out := make([]string, 0, len(j.Unwrap()))
		for _, e := range j.Unwrap() {
			out = append(out, e.Error())
		}
		return out
	}
	return []string{err.Error()}
}

// Recommendation is the single highest-leverage change.
type Recommendation struct {
	Title    string `json:"title"`
	Evidence string `json:"evidence"`
	Action   string `json:"action"`
}

// DX is what sessions are like to work with, graded.
type DX struct {
	Window         string          `json:"window"`
	Metrics        agentdx.Metrics `json:"metrics"`
	Grades         agentdx.Grades  `json:"grades"`
	Recommendation *Recommendation `json:"recommendation,omitempty"`
	Note           string          `json:"note,omitempty"`
	// Warnings names each client whose transcripts could not be read.
	// The metrics cover the others; failing the whole call instead hid
	// the cause behind "internal error".
	Warnings []string `json:"warnings,omitempty"`
}

// ComputeDX measures turns and wall-clock per instruction, rework,
// interrupts, escalation, first-try rate, context growth and compactions,
// grades each, and names the single change worth making.
func ComputeDX(w Window, now time.Time) DX {
	opts, window := w.options(now, false)
	records, err := agentdx.ExtractAll(opts)
	m := agentdx.ComputeByProvider(records)
	out := DX{Window: window, Metrics: m, Grades: agentdx.Grade(m), Warnings: readWarnings(err)}
	if m.Prompts == 0 {
		out.Note = "no instructions in this window — widen with days or all: true, or check the transcript root"
		return out
	}
	if rec, ok := agentdx.Recommend(m); ok {
		out.Recommendation = &Recommendation{Title: rec.Title, Evidence: rec.Evidence, Action: rec.Action}
	} else {
		out.Note = "nothing stands out — every measured dimension grades well"
	}
	return out
}

// Task is one piece of work: a run of consecutive instructions on it.
type Task struct {
	// Title is the operator's own instruction, quoted. Empty when the
	// caller withheld prompt text.
	Title        string   `json:"title,omitempty"`
	SessionID    string   `json:"session_id"`
	Provider     string   `json:"provider,omitempty"`
	Start        string   `json:"start"`
	End          string   `json:"end"`
	DurationSec  float64  `json:"duration_seconds"`
	Boundary     string   `json:"boundary"`
	Instructions int      `json:"instructions"`
	Turns        int      `json:"turns"`
	ToolCalls    int      `json:"tool_calls"`
	PeakContext  int64    `json:"peak_context_tokens"`
	Files        []string `json:"files,omitempty"`
	Frictions    []string `json:"frictions"`
	Clean        bool     `json:"clean"`
}

// Story is an account of recent work, newest task first.
type Story struct {
	Window string `json:"window"`
	Tasks  []Task `json:"tasks"`
	// TitlesWithheld is set when the operator's instructions were left
	// out, as the daemon API does: it serves derived figures, never
	// prompt text (ADR 0010 §5).
	TitlesWithheld bool   `json:"titles_withheld,omitempty"`
	Note           string `json:"note,omitempty"`
	// Warnings names each client whose transcripts could not be read.
	Warnings []string `json:"warnings,omitempty"`
}

// ComputeStory groups recent instructions into tasks, newest first, at
// most limit of them (limit zero means ten; negative means all). With
// titles, each task is titled with the operator's own instruction; the
// text is read at scan time and never persisted.
func ComputeStory(w Window, limit int, titles bool, now time.Time) Story {
	if limit == 0 {
		limit = 10
	}
	opts, window := w.options(now, titles)
	records, readErr := agentdx.ExtractAll(opts)
	tasks := story.Group(agentdx.Units(records), story.Options{})
	// Newest first: the work being asked about is the work just done.
	for i, j := 0, len(tasks)-1; i < j; i, j = i+1, j-1 {
		tasks[i], tasks[j] = tasks[j], tasks[i]
	}
	if limit > 0 && len(tasks) > limit {
		tasks = tasks[:limit]
	}
	out := Story{Window: window, Tasks: make([]Task, 0, len(tasks)), TitlesWithheld: !titles, Warnings: readWarnings(readErr)}
	for _, t := range tasks {
		found := t.Frictions()
		frictions := make([]string, 0, len(found))
		for _, f := range found {
			frictions = append(frictions, f.Detail)
		}
		task := Task{
			SessionID:    t.SessionID,
			Provider:     t.Provider,
			Start:        t.Start.Format(time.RFC3339),
			End:          t.End.Format(time.RFC3339),
			DurationSec:  t.Duration().Seconds(),
			Boundary:     string(t.Boundary),
			Instructions: t.Instructions(),
			Turns:        t.Turns(),
			ToolCalls:    t.ToolCalls(),
			PeakContext:  t.PeakContext(),
			Files:        t.Files(),
			Frictions:    frictions,
			Clean:        t.Clean(),
		}
		if titles {
			task.Title = t.Title
		}
		out.Tasks = append(out.Tasks, task)
	}
	if len(out.Tasks) == 0 {
		out.Note = "no tasks in this window — widen with days or all: true, or check the transcript root"
	}
	return out
}
