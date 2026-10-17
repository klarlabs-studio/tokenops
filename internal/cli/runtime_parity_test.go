package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/mcp"
)

// The cohort fixture is fictional: one Codex machine running three
// automatic reviews beside two pieces of the operator's own work, one of
// which was never answered. The instruction text, the path and the session
// id carry markers no derived export may repeat.
const (
	secretPrompt  = "zebra-quartz instruction text"
	secretPath    = "/home/fictional/zebra-repo/main.go"
	secretSession = "0f0f0f0f-zebra-session"
)

var cohortFixture = map[string][]string{
	"2026/01/05/rollout-2026-01-05T09-00-00-review1.jsonl": reviewRollout("2026-01-05T09:00", "review this diff"),
	"2026/01/05/rollout-2026-01-05T09-10-00-review2.jsonl": reviewRollout("2026-01-05T09:10", "review this diff"),
	"2026/01/05/rollout-2026-01-05T09-20-00-review3.jsonl": reviewRollout("2026-01-05T09:20", "review this diff"),
	"2026/01/05/rollout-2026-01-05T10-00-00-" + secretSession + ".jsonl": {
		`{"timestamp":"2026-01-05T10:00:00Z","type":"session_meta","payload":{"source":"vscode","cwd":"` + secretPath + `"}}`,
		`{"timestamp":"2026-01-05T10:00:00Z","type":"turn_context","payload":{"model":"gpt-fictional"}}`,
		`{"timestamp":"2026-01-05T10:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + secretPrompt + `"}]}}`,
		`{"timestamp":"2026-01-05T10:00:10Z","type":"response_item","payload":{"type":"function_call","call_id":"c1","name":"shell","arguments":"{\"path\":\"` + secretPath + `\"}"}}`,
		`{"timestamp":"2026-01-05T10:00:11Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"c2","name":"apply_patch","input":"` + secretPath + `"}}`,
		`{"timestamp":"2026-01-05T10:00:40Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1"}}`,
		`{"timestamp":"2026-01-05T10:00:41Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c2"}}`,
		`{"timestamp":"2026-01-05T10:01:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[]}}`,
		`{"timestamp":"2026-01-05T10:01:01Z","type":"event_msg","payload":{"type":"task_complete","duration_ms":60000,"time_to_first_token_ms":1500}}`,
	},
	"2026/01/06/rollout-2026-01-06T08-00-00-unanswered.jsonl": {
		`{"timestamp":"2026-01-06T08:00:00Z","type":"session_meta","payload":{"source":"vscode"}}`,
		`{"timestamp":"2026-01-06T08:00:00Z","type":"turn_context","payload":{"model":"gpt-fictional"}}`,
		`{"timestamp":"2026-01-06T08:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"are you still there"}]}}`,
	},
}

func reviewRollout(minute, text string) []string {
	return []string{
		`{"timestamp":"` + minute + `:00Z","type":"session_meta","payload":{"source":{"subagent":{"other":"guardian"}}}}`,
		`{"timestamp":"` + minute + `:00Z","type":"turn_context","payload":{"model":"codex-auto-review"}}`,
		`{"timestamp":"` + minute + `:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + text + `"}]}}`,
		`{"timestamp":"` + minute + `:04Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[]}}`,
		`{"timestamp":"` + minute + `:04Z","type":"event_msg","payload":{"type":"task_complete","duration_ms":3000,"time_to_first_token_ms":800}}`,
	}
}

func writeCohortFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, lines := range cohortFixture {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// cohortServer is the public tokenops_sessions tool over the fixture, as
// serve registers it.
func cohortServer(t *testing.T, root string) *mcp.Server {
	t.Helper()
	srv := mcp.NewServer("tokenops", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := mcp.RegisterStoryTools(srv, mcp.StoryDeps{Root: root}); err != nil {
		t.Fatal(err)
	}
	if err := mcp.RegisterAgentDXTools(srv, mcp.AgentDXDeps{Root: root}); err != nil {
		t.Fatal(err)
	}
	return srv
}

func callTool(t *testing.T, srv *mcp.Server, name string, args map[string]any) string {
	t.Helper()
	tool, ok := srv.GetTool(name)
	if !ok {
		t.Fatalf("no tool %q", name)
	}
	in, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if s, ok := res.(string); ok {
		return s
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func decodeAny(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return out
}

// The same cohort, asked for through the CLI and through the MCP tool,
// is the same answer: the filters are one selection, not two
// implementations of it.
func TestRuntimeCohortIsTheSameThroughCLIAndMCP(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := writeCohortFixture(t)
	srv := cohortServer(t, root)

	for _, role := range []string{"all", "primary", "auto_review"} {
		t.Run(role, func(t *testing.T) {
			cli, err := executeRoot(t, "story", "--runtime", "--json", "--root", root, "--source", "codex",
				"--days", "0", "--limit", "0", "--role", role)
			if err != nil {
				t.Fatal(err)
			}
			tool := callTool(t, srv, "tokenops_task_runtime",
				map[string]any{"all": true, "limit": -1, "source": "codex", "role": role})
			sameAnswer(t, "runtime "+role, decodeAny(t, cli), decodeAny(t, tool))

			cliDX, err := executeRoot(t, "dx", "--fresh", "--json", "--root", root, "--source", "codex",
				"--days", "0", "--role", role)
			if err != nil {
				t.Fatal(err)
			}
			toolDX := callTool(t, srv, "tokenops_agent_dx", map[string]any{"all": true, "source": "codex", "role": role})
			sameAnswer(t, "dx "+role, decodeAny(t, cliDX), decodeAny(t, toolDX))
		})
	}
}

// Filtering separates the automatic reviews from the operator's work, and
// every summary states its denominators.
func TestRuntimeFiltersAndDenominators(t *testing.T) {
	root := writeCohortFixture(t)
	srv := cohortServer(t, root)
	get := func(role string) runtimeDoc {
		var doc runtimeDoc
		raw := callTool(t, srv, "tokenops_task_runtime", map[string]any{"all": true, "limit": -1, "source": "codex", "role": role})
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}

	all := get("all")
	if all.Cohort.InWindow != 5 || all.Cohort.ByRole["auto_review"] != 3 || all.Cohort.ByRole["primary"] != 2 {
		t.Errorf("cohort = %+v, want 5 instructions: 3 reviews, 2 primary", all.Cohort)
	}
	// Reviews, the answered work, and the unanswered instruction, which no
	// turn served and so has no model.
	if len(all.Groups) != 3 {
		t.Errorf("groups = %d, want reviews and work apart", len(all.Groups))
	} else if all.Groups[0].Tasks != 3 {
		t.Errorf("largest group = %d tasks, want the 3 reviews", all.Groups[0].Tasks)
	}

	reviews := get("auto_review")
	if s := reviews.Summary; s.Tasks != 3 || s.RuntimeSamples != 3 || s.Status.Completed != 3 {
		t.Errorf("reviews summary = %+v", s)
	}
	if m := reviews.Summary.Observed; m.N != 3 || *m.Median != 3 || m.Method != "nearest-rank" {
		t.Errorf("review intervals = %+v, want three 3s samples", m)
	}

	work := get("primary")
	s := work.Summary
	if s.Tasks != 2 || s.RuntimeSamples != 1 || s.ExcludedNoTurns != 1 || s.ExcludedOpen != 0 {
		t.Errorf("work denominators = %+v, want 2 tasks: 1 sample, 1 never answered", s)
	}
	if *s.Observed.Median != 60 {
		t.Errorf("work median = %v, want 60", *s.Observed.Median)
	}
	for _, c := range s.Categories {
		if c.Category == "model_request" && (c.Known != 0 || c.Unknown != 1) {
			t.Errorf("model_request = %+v, want unknown for codex", c)
		}
	}
	var answered *runtimeTask
	for i := range work.Tasks {
		if work.Tasks[i].ObservedSeconds != nil {
			answered = &work.Tasks[i]
		}
	}
	if answered == nil {
		t.Fatal("no answered task")
	}
	// Two calls overlapping: 31s of wall time, 60s of tool time.
	if *answered.Tools.CriticalPathSeconds != 31 || *answered.Tools.ResourceSeconds != 60 {
		t.Errorf("tools = critical %v resource %v, want 31 and 60", *answered.Tools.CriticalPathSeconds, *answered.Tools.ResourceSeconds)
	}
	if answered.Status != "completed" || answered.HarnessTTFTSeconds == nil || *answered.HarnessTTFTSeconds != 1.5 {
		t.Errorf("answered task = %+v", answered)
	}
}

type runtimeDoc struct {
	Cohort struct {
		InWindow int            `json:"instructions_in_window"`
		ByRole   map[string]int `json:"instructions_by_role"`
	} `json:"cohort"`
	Tasks   []runtimeTask    `json:"tasks"`
	Summary runtimeSummary   `json:"summary"`
	Groups  []runtimeSummary `json:"groups"`
}

type runtimeTask struct {
	Status             string   `json:"status"`
	ObservedSeconds    *float64 `json:"observed_seconds"`
	HarnessTTFTSeconds *float64 `json:"harness_ttft_seconds"`
	Tools              struct {
		CriticalPathSeconds *float64 `json:"critical_path_seconds"`
		ResourceSeconds     *float64 `json:"resource_seconds"`
	} `json:"tools"`
}

type runtimeDist struct {
	N      int      `json:"n"`
	Median *float64 `json:"median"`
	Method string   `json:"method"`
}

type runtimeSummary struct {
	Tasks           int `json:"tasks"`
	RuntimeSamples  int `json:"runtime_samples"`
	ExcludedOpen    int `json:"excluded_open"`
	ExcludedNoTurns int `json:"excluded_no_turns"`
	Status          struct {
		Completed int `json:"completed"`
	} `json:"status"`
	Observed   runtimeDist `json:"observed_seconds"`
	Categories []struct {
		Category string `json:"category"`
		Known    int    `json:"known"`
		Unknown  int    `json:"unknown"`
	} `json:"categories"`
}

// The export is derived figures only: no instruction text, no path, no
// session id, through either surface.
func TestRuntimeExportCarriesNoRawText(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := writeCohortFixture(t)
	cli, err := executeRoot(t, "story", "--runtime", "--json", "--root", root, "--source", "codex", "--days", "0", "--limit", "0")
	if err != nil {
		t.Fatal(err)
	}
	text, err := executeRoot(t, "story", "--runtime", "--root", root, "--source", "codex", "--days", "0", "--limit", "0")
	if err != nil {
		t.Fatal(err)
	}
	tool := callTool(t, cohortServer(t, root), "tokenops_task_runtime", map[string]any{"all": true, "limit": -1, "source": "codex"})
	for name, out := range map[string]string{"cli json": cli, "cli text": text, "mcp": tool} {
		for _, secret := range []string{"zebra", "fictional/", "review this diff", "are you still there"} {
			if strings.Contains(out, secret) {
				t.Errorf("%s export leaks %q", name, secret)
			}
		}
	}
}
