package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// The public tokenops_sessions tool carries the cohort filters to the
// runtime view, and refuses a role it does not know rather than quietly
// measuring everything.
func TestSessionsRuntimeViewTakesTheCohort(t *testing.T) {
	lines := []string{
		`{"type":"user","timestamp":"2099-01-01T10:00:00Z","sessionId":"s","message":{"content":"fictional instruction"}}`,
		`{"type":"assistant","timestamp":"2099-01-01T10:00:05Z","sessionId":"s","requestId":"r1","message":{"model":"model-a","content":[{"type":"text","text":"ok"}]}}`,
	}
	srv := storyServer(t, strings.Join(lines, "\n"))
	if err := Consolidate(srv); err != nil {
		t.Fatal(err)
	}
	out := execTool(t, srv, "tokenops_sessions", map[string]any{"view": "runtime", "all": true, "model": "MODEL-A", "role": "primary"})
	var res struct {
		View    string `json:"view"`
		Runtime struct {
			Cohort struct {
				Model    string `json:"model"`
				Role     string `json:"role"`
				Selected int    `json:"instructions_selected"`
			} `json:"cohort"`
			Summary struct {
				Tasks int `json:"tasks"`
			} `json:"summary"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if res.View != "runtime" || res.Runtime.Cohort.Role != "primary" || res.Runtime.Cohort.Model != "MODEL-A" ||
		res.Runtime.Cohort.Selected != 1 || res.Runtime.Summary.Tasks != 1 {
		t.Errorf("runtime view = %s", out)
	}
	if strings.Contains(out, "fictional instruction") {
		t.Errorf("runtime view carries the instruction text")
	}
	if err := execToolErr(t, srv, "tokenops_sessions", map[string]any{"view": "runtime", "role": "reviewer"}); err == nil {
		t.Errorf("an unknown role was accepted")
	}
	// The runtime report is derived figures only: quote opens nothing.
	if err := execToolErr(t, srv, "tokenops_sessions", map[string]any{"view": "runtime", "quote": true}); err == nil {
		t.Errorf("quote was accepted on the runtime view")
	}
}

// The story view keeps both halves: the cohort filters and the quote
// opt-in, with the instruction text withheld unless asked for.
func TestSessionsStoryViewTakesTheCohortAndWithholdsByDefault(t *testing.T) {
	lines := []string{
		`{"type":"user","timestamp":"2099-01-01T10:00:00Z","sessionId":"s","message":{"content":"fictional instruction"}}`,
		`{"type":"assistant","timestamp":"2099-01-01T10:00:05Z","sessionId":"s","requestId":"r1","message":{"model":"model-a","content":[{"type":"text","text":"ok"}]}}`,
	}
	srv := storyServer(t, strings.Join(lines, "\n"))
	if err := Consolidate(srv); err != nil {
		t.Fatal(err)
	}
	derived := execTool(t, srv, "tokenops_sessions", map[string]any{"view": "story", "all": true, "role": "primary"})
	if strings.Contains(derived, "fictional instruction") || !strings.Contains(derived, `"primary"`) {
		t.Errorf("default story = %s", derived)
	}
	quoted := execTool(t, srv, "tokenops_sessions", map[string]any{"view": "story", "all": true, "role": "primary", "quote": true})
	if !strings.Contains(quoted, "fictional instruction") {
		t.Errorf("quoted story = %s", quoted)
	}
}
