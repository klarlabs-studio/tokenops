package mcp

import (
	"context"
	"errors"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/sessions"
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

// storyResult is the sessions capability's payload, shared with the
// daemon API (ADR 0010 §4). Here, unlike the API, tasks carry their
// titles: the agent reading them is the operator's own.
type storyResult = sessions.Story

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
			res := sessions.ComputeStory(sessions.Window{Root: d.Root, Days: in.Days, All: in.All}, in.Limit, true, time.Now())
			return &res, nil
		})
	return nil
}
