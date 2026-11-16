package teamshare

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/allocation"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// Models a machine ran that no catalog prices: names its owner made up,
// which must not cross.
const (
	fineTune    = "ft:gpt-4o-mini:acme:secret-project:abc123"
	routerLocal = "acme-router-local-v2"
)

// modelDeps is spendDeps with each turn's model and agent: a dated catalog
// model in Claude Code, a fine-tune in opencode, and a router's own model
// through the proxy.
func modelDeps() Deps {
	d := spendDeps()
	turns, _ := d.Turns(context.Background(), time.Time{})
	turns[0].Model, turns[0].Source = "claude-opus-4-8-20261001", "claude-code-jsonl"
	turns[1].Model, turns[1].Source = fineTune, "opencode-sqlite"
	turns[2].Model, turns[2].Source = routerLocal, "proxy"
	d.Turns = func(context.Context, time.Time) ([]analytics.SessionTurn, error) { return turns, nil }
	return d
}

// A schema 6 upload splits its spend rows by model and agent. A model
// crosses only as a catalog ID, normalised; one the catalog does not
// price crosses as "other". The rows add up to the spend rows.
func TestSchema6SpendByModel(t *testing.T) {
	u, notes := Build(context.Background(), modelDeps(),
		Options{Days: 14, RepoNames: RepoRemote, Schema: teamwire.SchemaSeats}, now)
	if len(notes.Dropped) > 0 {
		t.Fatalf("dropped: %v", notes.Dropped)
	}
	if u.Schema != teamwire.SchemaSeats {
		t.Fatalf("schema = %d", u.Schema)
	}
	if err := u.Validate(); err != nil {
		t.Fatalf("schema 6 upload does not validate: %v", err)
	}
	want := []teamwire.ModelSpend{
		{Repo: "acme/api", Provider: "anthropic", Model: "claude-opus-4-8", Agent: "claude-code"},
		{Repo: "acme/api", Provider: "anthropic", Model: teamwire.ModelOther, Agent: "opencode"},
		{Repo: teamwire.RepoNone, Provider: "openai", Model: teamwire.ModelOther, Agent: teamwire.AgentOther},
	}
	if len(u.SpendModels) != len(want) {
		t.Fatalf("spend by model = %+v", u.SpendModels)
	}
	for i, w := range want {
		g := u.SpendModels[i]
		if g.Repo != w.Repo || g.Provider != w.Provider || g.Model != w.Model || g.Agent != w.Agent {
			t.Errorf("row %d = %+v; want %+v", i, g, w)
		}
	}

	// No model name outside the catalog: every model is a catalog ID or
	// "other", and nothing of the names the machine used is anywhere.
	raw, _ := json.Marshal(u)
	var wire struct {
		SpendModels []teamwire.ModelSpend `json:"spend_models"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	for _, r := range wire.SpendModels {
		if !teamwire.ValidModel(r.Provider, r.Model) {
			t.Errorf("model %q crosses, not in the catalog", r.Model)
		}
	}
	for _, c := range []string{fineTune, "acme:", "secret-project", routerLocal, "20261001", "sess-1", "/Users/alice", "hunter2"} {
		if strings.Contains(string(raw), c) {
			t.Errorf("upload carries %q:\n%s", c, raw)
		}
	}

	// Asked for schema 5, the same machine sends no split.
	u5, _ := Build(context.Background(), modelDeps(), Options{Days: 14, RepoNames: RepoRemote, Schema: teamwire.SchemaPrevented}, now)
	if u5.SpendModels != nil {
		t.Errorf("schema 5 upload carries spend by model: %+v", u5.SpendModels)
	}
	if err := u5.Validate(); err != nil {
		t.Errorf("schema 5 upload does not validate: %v", err)
	}
}

// The model rows are the spend rows split further: the same turns, tokens
// and dollars per day, repository and provider.
func TestModelSpendRowsAddUpToSpendRows(t *testing.T) {
	d := modelDeps()
	turns, _ := d.Turns(context.Background(), time.Time{})
	repoOf := map[string]string{"sess-1": "acme/api"}
	covered := map[string]bool{now.Format(teamwire.DayLayout): true}
	type key struct{ day, repo, provider string }
	sums := map[key]teamwire.SpendBucket{}
	for _, r := range ModelSpendRows(turns, repoOf, covered) {
		k := key{r.Day, r.Repo, r.Provider}
		s := sums[k]
		s.Turns, s.Tokens, s.UnpricedTurns = s.Turns+r.Turns, s.Tokens+r.Tokens, s.UnpricedTurns+r.UnpricedTurns
		s.CostUSD, s.APIEquivalentUSD = s.CostUSD+r.CostUSD, s.APIEquivalentUSD+r.APIEquivalentUSD
		sums[k] = s
	}
	spend := SpendRows(turns, repoOf, covered)
	if len(spend) == 0 || len(spend) != len(sums) {
		t.Fatalf("spend %+v, sums %+v", spend, sums)
	}
	for _, s := range spend {
		got := sums[key{s.Day, s.Repo, s.Provider}]
		got.Day, got.Repo, got.Provider = s.Day, s.Repo, s.Provider
		if got != s {
			t.Errorf("model rows add up to %+v; spend row %+v", got, s)
		}
	}
}

// The agents are the harnesses allocation files a turn's source under,
// and "other".
func TestAgentsAreTheHarnesses(t *testing.T) {
	agents := slices.DeleteFunc(slices.Clone(teamwire.Agents), func(a string) bool { return a == teamwire.AgentOther })
	harnesses := allocation.Harnesses()
	slices.Sort(agents)
	slices.Sort(harnesses)
	if !slices.Equal(agents, harnesses) {
		t.Errorf("teamwire.Agents %v; allocation's harnesses %v", agents, harnesses)
	}
	for source, want := range map[string]string{
		"claude-code-jsonl": "claude-code", "codex-sessions": "codex", "pi-sessions": "pi",
		"github-copilot-chat": "copilot", "proxy": teamwire.AgentOther, "": teamwire.AgentOther,
		"/Users/alice/tool": teamwire.AgentOther,
	} {
		if got := AgentOf(source); got != want {
			t.Errorf("AgentOf(%q) = %q; want %q", source, got, want)
		}
	}
}
