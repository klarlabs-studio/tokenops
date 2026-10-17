// Package sessions answers what agent sessions are like to work with and
// what happened in them, from the transcripts the clients already write.
// The MCP tools, the daemon API and the dx, story and verify commands all
// call it (ADR 0010); it is the agent-DX capability ADR 0010 calls
// capability/dx.
package sessions

import (
	"strconv"
	"strings"
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
	// Source names one client to read (claude-code, codex, cursor,
	// opencode); empty reads every client present.
	Source string
	// IncludeScratch keeps sessions run in throwaway directories, which
	// are left out by default because they have no operator.
	IncludeScratch bool
	// Cache keeps what each transcript yielded between reads of the
	// window, so a caller reading it repeatedly (the daemon) reads only
	// what changed. Nil reads every transcript.
	Cache *TranscriptCache
	// Model keeps instructions mostly served by one model (compared
	// without regard to case); empty keeps every model.
	Model string
	// Role keeps instructions of one role: primary (the operator's own),
	// auto_review (a review the harness ran by itself) or subagent.
	// Empty or "all" keeps every role.
	Role string
}

// Cohort is the instruction filter the window applies after reading.
func (w Window) Cohort() (agentdx.Cohort, error) {
	role, err := agentdx.ParseRole(w.Role)
	if err != nil {
		return agentdx.Cohort{}, err
	}
	return agentdx.Cohort{Model: strings.TrimSpace(w.Model), Role: role}, nil
}

// Filtered reports whether the window selects a subset of instructions,
// which a precomputed whole-window answer cannot serve.
func (w Window) Filtered() bool {
	c, err := w.Cohort()
	return err != nil || !c.IsZero() || (w.Source != "" && w.Source != string(agentdx.SourceAuto))
}

// CohortInfo says which instructions a measurement covers and what the
// window held before the filter, so a pooled figure can be read for what
// it pools.
type CohortInfo struct {
	Source string `json:"source,omitempty"`
	Model  string `json:"model,omitempty"`
	Role   string `json:"role,omitempty"`
	// InWindow counts the window's instructions before the model and role
	// filter; ByRole splits them; Selected is how many the filter kept.
	InWindow int            `json:"instructions_in_window"`
	ByRole   map[string]int `json:"instructions_by_role"`
	Selected int            `json:"instructions_selected"`
}

// read extracts the window, applies the cohort, and says what it pooled.
// An invalid role is an error before anything is read.
func (w Window) read(now time.Time, withText bool) ([]agentdx.Record, string, CohortInfo, error) {
	opts, window := w.options(now, withText)
	cohort, err := w.Cohort()
	if err != nil {
		return nil, window, CohortInfo{}, err
	}
	records, readErr := agentdx.ExtractAll(opts)
	all := agentdx.Units(records)
	info := CohortInfo{Model: cohort.Model, Role: cohort.Role, InWindow: len(all), ByRole: agentdx.RoleCounts(all)}
	if w.Source != string(agentdx.SourceAuto) {
		info.Source = w.Source
	}
	records = agentdx.FilterRecords(records, cohort)
	info.Selected = len(agentdx.FilterUnits(all, cohort))
	return records, window, info, readErr
}

// TranscriptCache is the agent-DX domain's per-file transcript cache.
type TranscriptCache = agentdx.TranscriptCache

// NewTranscriptCache returns an empty transcript cache for Window.Cache.
func NewTranscriptCache() *TranscriptCache { return agentdx.NewTranscriptCache() }

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
	opts := agentdx.ExtractOptions{
		Root: w.Root, Source: agentdx.Source(w.Source),
		WithPromptText: withPromptText, IncludeScratch: w.IncludeScratch,
		Cache: w.Cache,
	}
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

// The agent-DX domain's types, aliased so it stays their single
// definition and a surface can render them without reaching into it.
type (
	// Metrics is what instructions cost and how often they went wrong.
	Metrics = agentdx.Metrics
	// Grades grades each metric.
	Grades = agentdx.Grades
	// Letter is one grade.
	Letter = agentdx.Letter
	// ContextBand is first-try success at one band of context size.
	ContextBand = agentdx.ContextBand
	// EffortRow is one model and effort level's figures.
	EffortRow = agentdx.EffortRow
	// Unit is one instruction and everything the agent did for it.
	Unit = agentdx.Unit
)

// MinEffortInstructions is how many instructions a model and effort
// level needs before its row is worth comparing.
const MinEffortInstructions = agentdx.MinEffortInstructions

// DegradationNote names how quality falls off as context grows, or is
// empty when it does not.
func DegradationNote(bands []ContextBand) string { return agentdx.DegradationNote(bands) }

// Units reads the instructions in w, with the operator's own words, for
// the surfaces that group or reconstruct work from them. The text is read
// at scan time and never persisted. A client whose transcripts could not
// be read is reported in the error, which joins one per client; whatever
// the others yielded is still returned.
func Units(w Window, now time.Time) ([]Unit, error) {
	records, _, _, err := w.read(now, true)
	return agentdx.Units(records), err
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
	Metrics        Metrics         `json:"metrics"`
	Grades         Grades          `json:"grades"`
	Recommendation *Recommendation `json:"recommendation,omitempty"`
	// Cohort is which instructions the metrics cover, and the role mix
	// of the window they were taken from.
	Cohort *CohortInfo `json:"cohort,omitempty"`
	Note   string      `json:"note,omitempty"`
	// Warnings names each client whose transcripts could not be read.
	// The metrics cover the others; failing the whole call instead hid
	// the cause behind "internal error".
	Warnings []string `json:"warnings,omitempty"`
}

// ComputeDX measures turns and wall-clock per instruction, rework,
// interrupts, escalation, first-try rate, context growth and compactions,
// grades each, and names the single change worth making.
func ComputeDX(w Window, now time.Time) DX {
	dx, _ := ComputeDXWithCurve(w, now)
	return dx
}

// ComputeDXWithCurve is ComputeDX, plus how first-try success changes as
// context grows, from the same read of the transcripts.
func ComputeDXWithCurve(w Window, now time.Time) (DX, []agentdx.ContextBand) {
	records, window, info, err := w.read(now, false)
	dx := dxOf(records, err, window)
	dx.Cohort = &info
	return dx, agentdx.ComputeContextCurve(records)
}

// DXFromRecords grades records already read, for a caller that needed
// the transcripts for more than the grade and should not read them twice.
func DXFromRecords(records []agentdx.Record, err error, window string) DX {
	return dxOf(records, err, window)
}

func dxOf(records []agentdx.Record, err error, window string) DX {
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
	Title        string  `json:"title,omitempty"`
	SessionID    string  `json:"session_id"`
	Provider     string  `json:"provider,omitempty"`
	Start        string  `json:"start"`
	End          string  `json:"end"`
	DurationSec  float64 `json:"duration_seconds"`
	Boundary     string  `json:"boundary"`
	Instructions int     `json:"instructions"`
	Turns        int     `json:"turns"`
	ToolCalls    int     `json:"tool_calls"`
	// ContextCarried is the context tokens every turn of the task carried,
	// summed: what it cost, where PeakContext is how large it grew.
	ContextCarried int64    `json:"context_carried_tokens"`
	PeakContext    int64    `json:"peak_context_tokens"`
	Files          []string `json:"files,omitempty"`
	Frictions      []string `json:"frictions"`
	Clean          bool     `json:"clean"`
}

// Story is an account of recent work, newest task first.
type Story struct {
	Window string `json:"window"`
	Tasks  []Task `json:"tasks"`
	// TitlesWithheld is set when the operator's instructions were left
	// out, as the daemon API does: it serves derived figures, never
	// prompt text (ADR 0010 §5).
	TitlesWithheld bool `json:"titles_withheld,omitempty"`
	// Cohort is which instructions the tasks were built from.
	Cohort *CohortInfo `json:"cohort,omitempty"`
	Note   string      `json:"note,omitempty"`
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
	records, window, info, readErr := w.read(now, titles)
	tasks := story.Group(agentdx.Units(records), story.Options{})
	// Newest first: the work being asked about is the work just done.
	for i, j := 0, len(tasks)-1; i < j; i, j = i+1, j-1 {
		tasks[i], tasks[j] = tasks[j], tasks[i]
	}
	if limit > 0 && len(tasks) > limit {
		tasks = tasks[:limit]
	}
	out := Story{Window: window, Tasks: make([]Task, 0, len(tasks)), TitlesWithheld: !titles,
		Cohort: &info, Warnings: readWarnings(readErr)}
	for _, t := range tasks {
		found := t.Frictions()
		frictions := make([]string, 0, len(found))
		for _, f := range found {
			frictions = append(frictions, f.Detail)
		}
		task := Task{
			SessionID:      t.SessionID,
			Provider:       t.Provider,
			Start:          t.Start.Format(time.RFC3339),
			End:            t.End.Format(time.RFC3339),
			DurationSec:    t.Duration().Seconds(),
			Boundary:       string(t.Boundary),
			Instructions:   t.Instructions(),
			Turns:          t.Turns(),
			ToolCalls:      t.ToolCalls(),
			ContextCarried: t.ContextCarried(),
			PeakContext:    t.PeakContext(),
			Files:          t.Files(),
			Frictions:      frictions,
			Clean:          t.Clean(),
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
