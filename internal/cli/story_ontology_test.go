package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
)

func storyTasks() []story.Task {
	t0 := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	return []story.Task{{
		Title: "ship the auth fix", SessionID: "sess-1", Provider: "anthropic",
		Start: t0, End: t0.Add(30 * time.Minute), Boundary: story.BoundaryIdle,
		Units: []agentdx.Unit{{SessionID: "sess-1", Start: t0, End: t0.Add(30 * time.Minute), Turns: 3}},
	}}
}

// An agent reading `story --json` got a list of tasks with no way to
// say what was a goal, what was an attempt at it, or whether anything
// judged the result. The ontology exists; this is what makes the
// reconstruction visible through it.
func TestStoryJSONCarriesTheOntology(t *testing.T) {
	var buf bytes.Buffer
	if err := writeStoryJSON(&buf, storyTasks(), 7); err != nil {
		t.Fatalf("writeStoryJSON: %v", err)
	}

	var got struct {
		Work []struct {
			Work struct {
				ID         string `json:"id"`
				Goal       string `json:"goal"`
				GoalSource string `json:"goal_source"`
				GoalCaveat string `json:"goal_caveat"`
			} `json:"work"`
			Execution struct {
				ID     string `json:"id"`
				Work   string `json:"work"`
				Status string `json:"status"`
			} `json:"execution"`
			Outcome struct {
				Result string `json:"result"`
				Caveat string `json:"caveat"`
			} `json:"outcome"`
		} `json:"work"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if len(got.Work) != 1 {
		t.Fatalf("want one reconstructed work, got %d:\n%s", len(got.Work), buf.String())
	}

	w := got.Work[0]
	if w.Work.Goal != "ship the auth fix" {
		t.Errorf("goal = %q", w.Work.Goal)
	}
	if w.Execution.Work != w.Work.ID {
		t.Errorf("the execution does not point at its work: %+v", w)
	}
	// The two claims an agent most needs to not confuse.
	if w.Work.GoalSource != "inferred" {
		t.Errorf("goal_source = %q; a reconstructed title is a guess", w.Work.GoalSource)
	}
	if w.Outcome.Result != "" {
		t.Errorf("outcome = %q; nothing assessed this work", w.Outcome.Result)
	}
	if w.Outcome.Caveat == "" {
		t.Error("the unknown outcome does not say why")
	}
}

// The existing shape is untouched, so anything already parsing this
// output keeps working.
func TestStoryJSONKeepsItsExistingShape(t *testing.T) {
	var buf bytes.Buffer
	if err := writeStoryJSON(&buf, storyTasks(), 7); err != nil {
		t.Fatalf("writeStoryJSON: %v", err)
	}
	for _, want := range []string{`"window_days"`, `"tasks"`, `"title"`, `"session_id"`, `"boundary"`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("the existing shape lost %s:\n%s", want, buf.String())
		}
	}
}

// An empty window emits an empty list rather than null, so a caller
// iterating the field does not have to special-case it.
func TestStoryJSONWithNoTasksEmitsAnEmptyList(t *testing.T) {
	var buf bytes.Buffer
	if err := writeStoryJSON(&buf, nil, 7); err != nil {
		t.Fatalf("writeStoryJSON: %v", err)
	}
	if !strings.Contains(buf.String(), `"work": []`) {
		t.Errorf("want an empty work list:\n%s", buf.String())
	}
}
