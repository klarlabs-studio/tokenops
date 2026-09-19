package mcp

import (
	"context"
	"errors"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
)

// StoryDeps wires the work-account tool. Root empty resolves the
// conventional transcript directory.
type StoryDeps struct {
	Root string
}

type storyInput struct {
	Days  int  `json:"days,omitempty" jsonschema:"description=Window in days (default 7 when omitted or 0). Use all for every transcript on disk."`
	All   bool `json:"all,omitempty" jsonschema:"description=Read all history instead of a days window. Overrides days."`
	Limit int  `json:"limit,omitempty" jsonschema:"description=Most recent tasks to return (default 10). 0 returns every task in the window."`
}

// storyTask is one piece of work. Enumerated rather than prose-formatted:
// this rendering exists for a reader that will do something with the
// fields, and the prose renderings live in the CLI.
type storyTask struct {
	Title        string   `json:"title"`
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

type storyResult struct {
	Window string      `json:"window"`
	Tasks  []storyTask `json:"tasks"`
	Note   string      `json:"note,omitempty"`
}

// RegisterStoryTools hands the agent its own history back.
//
// The account already existed, as JSON on stdout — which meant an agent
// could only read it by shelling out to its own telemetry, a thing agents
// do badly and operators find alarming. The facts are the same; what
// changes is that asking for them is now a tool call.
//
// What makes this worth a tool rather than a nicety: an agent that can
// read the account can see the shape of the work it is in the middle of.
// That the last four instructions were one task, that two of them were
// rejections, that it has already edited this file twice — each of those
// is a reason to ask a better question instead of pressing on, and none
// of them is visible from inside the context window.
func RegisterStoryTools(s *Server, d StoryDeps) error {
	if s == nil {
		return errors.New("mcp: nil server")
	}
	s.Tool("tokenops_story").
		Description("Read back an account of recent work, one task at a time: the instruction the operator typed, how many turns and tool calls answering it took, which files it touched, and where it went sideways — an answer rejected, a file edited twice, a turn interrupted. A task is a run of consecutive instructions on one piece of work, inferred from the local transcripts. Call this to see the shape of the work in flight, to check what was already attempted before retrying something, or when the operator asks what happened in a session. Titles are the operator's own instructions, quoted rather than summarised. Nothing here says whether the work is correct — only the tests know that.").
		OutputSchema(storyResult{}).
		Handler(func(_ context.Context, in storyInput) (*storyResult, error) {
			days := windowDays(in.Days, in.All)
			limit := in.Limit
			if limit == 0 {
				limit = 10
			}
			opts := agentdx.ExtractOptions{
				Root: d.Root,
				// The account is made of the operator's own words. This is
				// the only reason to carry prompt text, and it is read at
				// scan time and never persisted.
				WithPromptText: true,
			}
			window := "all history"
			if days > 0 {
				opts.Since = time.Now().AddDate(0, 0, -days)
				window = formatDays(days)
			}
			records, err := agentdx.ExtractAll(opts)
			if err != nil {
				return nil, err
			}
			tasks := story.Group(agentdx.Units(records), story.Options{})
			reverseTasks(tasks) // newest first: the work being asked about is the work just done
			if limit > 0 && len(tasks) > limit {
				tasks = tasks[:limit]
			}
			out := &storyResult{Window: window, Tasks: make([]storyTask, 0, len(tasks))}
			for _, t := range tasks {
				frictions := []string{}
				for _, f := range t.Frictions() {
					frictions = append(frictions, f.Detail)
				}
				out.Tasks = append(out.Tasks, storyTask{
					Title:        t.Title,
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
				})
			}
			if len(out.Tasks) == 0 {
				out.Note = "no tasks in this window — widen with days or all: true, or check the transcript root"
			}
			return out, nil
		})
	return nil
}

func reverseTasks(ts []story.Task) {
	for i, j := 0, len(ts)-1; i < j; i, j = i+1, j-1 {
		ts[i], ts[j] = ts[j], ts[i]
	}
}
