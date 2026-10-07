package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval"
	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	accountsapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/accounts"
	"go.klarlabs.de/tokenops/internal/mcp"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// The CLI and the MCP server answer the same questions. parity_test.go
// checks that every command has a tool; these check that the two give the
// same answer to the same question, over one set of fixtures.
//
// Name parity passed through every divergence #595–#598 fixed: a scorecard
// graded on fewer metrics, fmt reports blind to configured formatters, a
// usage-meter setup that dropped the clearance, routing proposals in a
// different shape. Each pair below decodes both answers and compares the
// fields they share. Where the shapes differ on purpose (the CLI wraps, the
// tool trims for an agent's context), the comparison is of the shared core,
// and the comment says why.

// parityFixture is one machine: a home with the sessions fixture's
// transcripts, an events store with the spend fixture's prompts and an
// audit entry, a config, and an approval log with one proposal.
type parityFixture struct {
	home, root, db, cfgPath, approvals string
	store                              *sqlite.Store
	srv                                *mcp.Server
}

func newParityFixture(t *testing.T, plans map[string]string) *parityFixture {
	t.Helper()
	f := &parityFixture{home: t.TempDir()}
	t.Setenv("HOME", f.home)
	t.Setenv("USERPROFILE", f.home)
	t.Setenv("TOKENOPS_STORAGE_PATH", "")
	f.root = writeSessionsFixture(t, filepath.Join(f.home, ".claude", "projects"))
	f.db = seedFixedSpendDB(t)
	store, err := sqlite.Open(context.Background(), f.db, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f.store = store
	if _, err := audit.NewRecorder(store).Record(context.Background(), audit.Entry{
		Action: audit.ActionBudgetExceeded, Actor: "parity", Target: "weekly",
		Details: map[string]any{"spent_usd": 150.0, "limit_usd": 100.0},
	}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Plans = plans
	cfg.Storage.Path = f.db
	f.cfgPath = filepath.Join(t.TempDir(), "config.yaml")
	if err := config.WriteMutable(f.cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	f.approvals = filepath.Join(t.TempDir(), "approvals.jsonl")
	log, err := routingapproval.Open(f.approvals)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Propose(routingapproval.Record{Key: "anthropic|claude-sonnet-5|claude-opus-5", Provider: "anthropic",
		From: "claude-sonnet-5", To: "claude-opus-5", Preferred: "claude-sonnet-5", Reason: "hard task"}); err != nil {
		t.Fatal(err)
	}

	eng := spend.NewEngine(spend.DefaultTable())
	srv := mcp.NewServer("tokenops", "test", nil)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	// Registered as serve registers them, minus Consolidate: the public
	// tools forward to these, so calling these is calling those.
	must(mcp.RegisterTools(srv, mcp.Deps{Store: store, Spend: eng, Aggregator: analytics.New(store, eng)}))
	must(mcp.RegisterParityTools(srv, mcp.ParityDeps{Store: store, Spend: eng}))
	must(mcp.RegisterStoryTools(srv, mcp.StoryDeps{Root: f.root}))
	must(mcp.RegisterAgentDXTools(srv, mcp.AgentDXDeps{Root: f.root}))
	must(mcp.RegisterCoachTools(srv, mcp.CoachDeps{JSONLRoot: f.root}))
	must(mcp.RegisterVerifyTool(srv, mcp.VerifyDeps{Store: store, Root: f.root}))
	must(mcp.RegisterFmtTools(srv, mcp.FmtDeps{ConfigGetter: func() *config.Config { return &cfg }}))
	must(mcp.RegisterGapTools(srv, mcp.GapDeps{}))
	must(mcp.RegisterApprovalTools(srv, mcp.ApprovalDeps{StorePath: f.approvals}))
	must(mcp.RegisterPlanTools(srv, mcp.PlanDeps{Config: &cfg, Store: store, Spend: eng}))
	f.srv = srv
	return f
}

// cli runs a command with the fixture's config and decodes its JSON.
func (f *parityFixture) cli(t *testing.T, out any, args ...string) {
	t.Helper()
	raw, err := executeRoot(t, append([]string{"--config", f.cfgPath}, args...)...)
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		t.Fatalf("%v: decode %q: %v", args, raw, err)
	}
}

// tool calls an MCP tool and decodes its JSON.
func (f *parityFixture) tool(t *testing.T, out any, name string, args map[string]any) {
	t.Helper()
	tool, ok := f.srv.GetTool(name)
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
	raw, ok := res.(string)
	if !ok {
		b, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		raw = string(b)
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		t.Fatalf("%s: decode %q: %v", name, raw, err)
	}
}

func sameAnswer(t *testing.T, what string, cli, tool any) {
	t.Helper()
	if !reflect.DeepEqual(cli, tool) {
		a, _ := json.MarshalIndent(cli, "", "  ")
		b, _ := json.MarshalIndent(tool, "", "  ")
		t.Errorf("%s: the CLI and the MCP tool disagree\n--- cli\n%s\n--- tool\n%s", what, a, b)
	}
}

var parityWindow = []string{"--since", "2026-01-01T00:00:00Z", "--until", "2026-01-08T00:00:00Z"}

func TestCLIAndMCPGiveTheSameAnswer(t *testing.T) {
	f := newParityFixture(t, map[string]string{"anthropic": "claude-max-20x"})
	toolWindow := map[string]any{"since": "2026-01-01T00:00:00Z", "until": "2026-01-08T00:00:00Z"}

	t.Run("spend totals", func(t *testing.T) {
		// The CLI reports the window's totals inside a report that also
		// ranks consumers; the tool answers the totals alone.
		type totals struct {
			Requests, InputTokens, OutputTokens, TotalTokens int64
			CostUSD, APIEquivalentUSD                        float64
		}
		var c struct {
			Summary totals `json:"summary"`
		}
		f.cli(t, &c, append([]string{"spend", "--db", f.db, "--json"}, parityWindow...)...)
		var m struct {
			Requests         int64   `json:"requests"`
			InputTokens      int64   `json:"input_tokens"`
			OutputTokens     int64   `json:"output_tokens"`
			TotalTokens      int64   `json:"total_tokens"`
			CostUSD          float64 `json:"cost_usd"`
			APIEquivalentUSD float64 `json:"api_equivalent_usd"`
		}
		f.tool(t, &m, "tokenops_spend_summary", toolWindow)
		sameAnswer(t, "spend totals", c.Summary, totals{m.Requests, m.InputTokens, m.OutputTokens, m.TotalTokens, m.CostUSD, m.APIEquivalentUSD})
	})

	t.Run("top consumers", func(t *testing.T) {
		// Same ranking; the CLI carries each row's full rollup, the tool
		// the figures an agent ranks by.
		type row struct {
			Key      string
			Requests int64
			Tokens   int64
			CostUSD  float64
		}
		var c struct {
			Top []struct {
				GroupKey    string
				Requests    int64
				TotalTokens int64
				CostUSD     float64
			} `json:"top"`
		}
		f.cli(t, &c, append([]string{"spend", "--db", f.db, "--json"}, parityWindow...)...)
		var m struct {
			Top []struct {
				Key      string  `json:"key"`
				Requests int64   `json:"requests"`
				Tokens   int64   `json:"tokens"`
				CostUSD  float64 `json:"cost_usd"`
			} `json:"top"`
		}
		f.tool(t, &m, "tokenops_top_consumers", toolWindow)
		a, b := make([]row, 0, len(c.Top)), make([]row, 0, len(m.Top))
		for _, r := range c.Top {
			a = append(a, row{r.GroupKey, r.Requests, r.TotalTokens, r.CostUSD})
		}
		for _, r := range m.Top {
			b = append(b, row{r.Key, r.Requests, r.Tokens, r.CostUSD})
		}
		sameAnswer(t, "top consumers", a, b)
	})

	t.Run("scorecard", func(t *testing.T) {
		// generated_at is each call's own clock.
		var c, m map[string]any
		f.cli(t, &c, "scorecard", "--db", f.db, "--json")
		f.tool(t, &m, "tokenops_scorecard", map[string]any{})
		delete(c, "generated_at")
		delete(m, "generated_at")
		sameAnswer(t, "scorecard", c, m)
	})

	t.Run("coach prompts", func(t *testing.T) {
		// The CLI adds the average turn's cost beside the findings.
		var c struct {
			Findings map[string]any `json:"findings"`
		}
		f.cli(t, &c, "coach", "prompts", "--root", f.root, "--source", "claude-code", "--since", "2026-01-01T00:00:00Z", "--json")
		var m map[string]any
		f.tool(t, &m, "tokenops_coach_prompts", map[string]any{"since": "2026-01-01T00:00:00Z"})
		sameAnswer(t, "coach prompts", c.Findings, m)
	})

	t.Run("story", func(t *testing.T) {
		// The CLI also carries each task as reconstructed work; the
		// tasks are the shared answer.
		var c, m struct {
			Tasks []map[string]any `json:"tasks"`
		}
		f.cli(t, &c, "story", "--root", f.root, "--source", "claude-code", "--days", "0", "--limit", "0", "--json")
		f.tool(t, &m, "tokenops_story", map[string]any{"all": true, "limit": -1})
		sameAnswer(t, "story", c.Tasks, m.Tasks)
	})

	t.Run("agent dx", func(t *testing.T) {
		var c, m map[string]any
		f.cli(t, &c, "dx", "--root", f.root, "--source", "claude-code", "--days", "0", "--fresh", "--json")
		f.tool(t, &m, "tokenops_agent_dx", map[string]any{"all": true})
		sameAnswer(t, "agent dx", c, m)
	})

	t.Run("verify", func(t *testing.T) {
		// The CLI prints the report; the tool reads it out for an agent.
		// The cohorts are the shared answer.
		type cohorts struct {
			Baseline, Intervention int
		}
		var c struct {
			BaselineCount     int `json:"baseline_count"`
			InterventionCount int `json:"intervention_count"`
		}
		f.cli(t, &c, "verify", "--db", f.db, "--days", "0", "--json")
		var m struct {
			BaselineCount     int `json:"baseline_count"`
			InterventionCount int `json:"intervention_count"`
		}
		f.tool(t, &m, "tokenops_verify", map[string]any{"all": true})
		if c.BaselineCount == 0 {
			t.Fatal("the fixture reconstructs no attempts; the comparison would be vacuous")
		}
		sameAnswer(t, "verify", cohorts{c.BaselineCount, c.InterventionCount}, cohorts{m.BaselineCount, m.InterventionCount})
	})

	t.Run("audit", func(t *testing.T) {
		var c, m map[string]any
		f.cli(t, &c, "audit", "--db", f.db, "--json")
		f.tool(t, &m, "tokenops_audit", map[string]any{})
		sameAnswer(t, "audit", c, m)
	})

	t.Run("plan headroom", func(t *testing.T) {
		// The CLI emits the reports; the tool wraps them with notes.
		var c []map[string]any
		f.cli(t, &c, "plan", "headroom", "--json")
		var m struct {
			Reports []map[string]any `json:"reports"`
		}
		f.tool(t, &m, "tokenops_plan_headroom", map[string]any{})
		sameAnswer(t, "plan headroom", c, m.Reports)
	})

	t.Run("routing proposals", func(t *testing.T) {
		var c, m map[string]any
		f.cli(t, &c, "routing", "proposals", "--store", f.approvals, "--json")
		f.tool(t, &m, "tokenops_routing_proposals", map[string]any{})
		sameAnswer(t, "routing proposals", c, m)
	})

	t.Run("pricing", func(t *testing.T) {
		// The CLI shows the whole snapshot; the tool a filtered page of
		// it. Every rate the tool returns must be the snapshot's.
		var c struct {
			Rates map[string]struct {
				Input  float64 `json:"input_per_million"`
				Output float64 `json:"output_per_million"`
			} `json:"rates"`
		}
		f.cli(t, &c, "pricing", "show", "--json")
		var m struct {
			Rates []struct {
				Model  string  `json:"model"`
				Input  float64 `json:"input_per_1m_usd"`
				Output float64 `json:"output_per_1m_usd"`
			} `json:"rates"`
		}
		f.tool(t, &m, "tokenops_pricing", map[string]any{"provider": "anthropic", "limit": 50})
		if len(m.Rates) == 0 {
			t.Fatal("the tool returned no anthropic rates")
		}
		for _, r := range m.Rates {
			got, ok := c.Rates[r.Model]
			if !ok || got.Input != r.Input || got.Output != r.Output {
				t.Errorf("%s: CLI %+v (present %v), tool in %v out %v", r.Model, got, ok, r.Input, r.Output)
			}
		}
	})

	t.Run("fmt analyze", func(t *testing.T) {
		// generated_at_unix is each call's own clock.
		var c, m map[string]any
		f.cli(t, &c, "fmt", "analyze", "--root", f.root, "--json")
		f.tool(t, &m, "tokenops_fmt_analyze", map[string]any{"root": f.root})
		delete(c, "generated_at_unix")
		delete(m, "generated_at_unix")
		sameAnswer(t, "fmt analyze", c, m)
	})
}

// With no plan bound, headroom comes from what the vendors report. The
// tool answered from those readings; the CLI refused with "no plans
// configured" before asking.
func TestPlanHeadroomWithoutABindingAnswersFromReadings(t *testing.T) {
	f := newParityFixture(t, nil)
	now := time.Now().UTC()
	if err := f.store.Append(context.Background(), accounts.NewEnvelope(now.Add(-time.Minute), accountsapi.Kimi{}, accounts.Reading{
		Scope: "account", Subscription: true, Windows: []accounts.Window{
			{Name: "week", UsedPct: 40, Duration: 7 * 24 * time.Hour, ResetsAt: now.Add(48 * time.Hour)},
		}})); err != nil {
		t.Fatal(err)
	}
	var m struct {
		Reports []map[string]any `json:"reports"`
	}
	f.tool(t, &m, "tokenops_plan_headroom", map[string]any{})
	if len(m.Reports) == 0 {
		t.Fatal("the tool reported nothing; the fixture is wrong")
	}
	var c []map[string]any
	f.cli(t, &c, "plan", "headroom", "--json")
	providers := func(rs []map[string]any) []string {
		out := make([]string, 0, len(rs))
		for _, r := range rs {
			out = append(out, r["provider"].(string))
		}
		sort.Strings(out)
		return out
	}
	sameAnswer(t, "headroom providers", providers(c), providers(m.Reports))
}
