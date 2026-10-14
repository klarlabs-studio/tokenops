package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

// A config written before budgets held several windows must load, map and
// write back exactly as it did: one window, soft, no boundary rule.
func TestSingleWindowBudgetConfigUnchanged(t *testing.T) {
	const legacy = `budgets:
  - name: eng
    window: monthly
    limit_usd: 200
    limit_tokens: 0
    warn_at: 0.5
    crit_at: 0
    workflow_id: ""
    agent_id: ""
    basis: ""
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadMutable(path)
	if err != nil {
		t.Fatalf("ReadMutable: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a single-window budget no longer validates: %v", err)
	}
	limits := cfg.BudgetLimits()
	if len(limits) != 1 {
		t.Fatalf("limits = %+v; want one", limits)
	}
	l := limits[0]
	if l.Name != "eng" || l.Key() != "eng" || l.Window != "monthly" || l.LimitUSD != 200 || l.WarnAt != 0.5 || l.Mode != "soft" {
		t.Errorf("limit = %+v (key %q); want eng, monthly, $200, soft, keyed by its name", l, l.Key())
	}
	if rules := cfg.BudgetRules(); len(rules) != 0 {
		t.Errorf("a soft budget made boundary rules: %+v", rules)
	}
	out, err := yaml.Marshal(struct {
		Budgets []BudgetConfig `yaml:"budgets"`
	}{cfg.Budgets})
	if err != nil {
		t.Fatal(err)
	}
	// The shape every budget was written in before windows existed.
	type before struct {
		Name        string  `yaml:"name"`
		Window      string  `yaml:"window"`
		LimitUSD    float64 `yaml:"limit_usd"`
		LimitTokens int64   `yaml:"limit_tokens"`
		WarnAt      float64 `yaml:"warn_at"`
		CritAt      float64 `yaml:"crit_at"`
		WorkflowID  string  `yaml:"workflow_id"`
		AgentID     string  `yaml:"agent_id"`
		Basis       string  `yaml:"basis"`
	}
	want, err := yaml.Marshal(struct {
		Budgets []before `yaml:"budgets"`
	}{[]before{{Name: "eng", Window: "monthly", LimitUSD: 200, WarnAt: 0.5}}})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(want) {
		t.Errorf("written back as\n%s\nwant\n%s", out, want)
	}
}

func TestBudgetWindowsValidate(t *testing.T) {
	ok := BudgetConfig{Name: "team", Windows: []BudgetWindowConfig{
		{Window: "daily", LimitUSD: 10, Mode: "hard"},
		{Window: "monthly", LimitUSD: 100},
	}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("$10/day hard and $100/month soft rejected: %v", err)
	}
	cases := []struct {
		name string
		edit func(*BudgetConfig)
		want string
	}{
		{"window beside windows", func(b *BudgetConfig) { b.Window = "weekly" }, "inside windows"},
		{"limit beside windows", func(b *BudgetConfig) { b.LimitUSD = 5 }, "inside windows"},
		{"duplicate window", func(b *BudgetConfig) { b.Windows[1].Window = "daily" }, "twice"},
		{"unknown window", func(b *BudgetConfig) { b.Windows[0].Window = "hourly" }, "window must be"},
		{"no ceiling", func(b *BudgetConfig) { b.Windows[1].LimitUSD = 0 }, "limit_usd"},
		{"unknown mode", func(b *BudgetConfig) { b.Windows[0].Mode = "strict" }, "mode"},
		{"require on soft", func(b *BudgetConfig) { b.Windows[1].Require = "deny" }, "hard"},
		{"require allow", func(b *BudgetConfig) { b.Windows[0].Require = "allow" }, "ask or deny"},
		{"unknown stage", func(b *BudgetConfig) { b.Windows[0].Stage = "live" }, "stage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := ok
			b.Windows = append([]BudgetWindowConfig(nil), ok.Windows...)
			tc.edit(&b)
			err := b.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want one naming %q", err, tc.want)
			}
		})
	}
}

func TestBudgetLimitsOnePerWindow(t *testing.T) {
	cfg := Config{Budgets: []BudgetConfig{{Name: "team", WarnAt: 0.6, Windows: []BudgetWindowConfig{
		{Window: "daily", LimitUSD: 10, Mode: "hard"},
		{Window: "Monthly", LimitUSD: 100},
	}}}}
	limits := cfg.BudgetLimits()
	if len(limits) != 2 {
		t.Fatalf("limits = %+v; want two", limits)
	}
	if l := limits[0]; l.Key() != "team:daily" || l.Name != "team" || l.LimitUSD != 10 || l.Mode != "hard" || l.WarnAt != 0.6 {
		t.Errorf("daily = %+v (key %q)", l, l.Key())
	}
	if l := limits[1]; l.Key() != "team:monthly" || l.Window != "monthly" || l.LimitUSD != 100 || l.Mode != "soft" {
		t.Errorf("monthly = %+v (key %q)", l, l.Key())
	}
}

// A hard window is enforced by a spend boundary on that window's figure,
// through the boundary engine like any rule, and starts in shadow.
func TestHardWindowIsASpendBoundary(t *testing.T) {
	cfg := Default()
	cfg.Budgets = []BudgetConfig{{Name: "Team Budget", Windows: []BudgetWindowConfig{
		{Window: "daily", LimitUSD: 10, Mode: "hard"},
		{Window: "monthly", LimitUSD: 100, Mode: "hard", Require: "deny", Stage: "enforce"},
		{Window: "weekly", LimitUSD: 40},
	}}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	rules := cfg.BoundaryRules()
	if len(rules) != 2 {
		t.Fatalf("rules = %+v; want one per hard window", rules)
	}
	daily, monthly := rules[0], rules[1]
	if daily.Name != "budget-team-budget-daily" || daily.Action != boundaries.ActionSpend ||
		daily.Require.Kind != boundaries.RequireAsk || daily.Enforced() {
		t.Errorf("daily rule = %+v; want an ask spend rule in shadow", daily)
	}
	if monthly.Require.Kind != boundaries.RequireDeny || !monthly.Enforced() {
		t.Errorf("monthly rule = %+v; want an enforced deny", monthly)
	}
	want := []boundaries.Condition{{Field: boundaries.FieldBudgetUsed, Op: boundaries.OpGE, Number: 100, Budget: "Team Budget:daily"}}
	if !reflect.DeepEqual(daily.When, want) {
		t.Errorf("daily when = %+v; want %+v", daily.When, want)
	}
}

func TestHardWindowRuleNameCollisionRefused(t *testing.T) {
	cfg := Default()
	cfg.Budgets = []BudgetConfig{{Name: "team", Window: "daily", LimitUSD: 10, Mode: "hard"}}
	cfg.Boundaries = []boundaries.RuleSpec{{Name: "budget-team-daily", Action: "continue", Require: "ask"}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "budget-team-daily") {
		t.Fatalf("err = %v; want the clash named", err)
	}
}

func TestUpsertBudgetAddsAndEditsWindows(t *testing.T) {
	c := Default()
	c.Budgets = []BudgetConfig{{Name: "team", Window: "monthly", LimitUSD: 100}}

	res, err := c.UpsertBudget(BudgetUpdate{Name: "team", Window: "daily", LimitUSD: 10, Mode: "hard", AddWindow: true})
	if err != nil {
		t.Fatalf("add window: %v", err)
	}
	want := []BudgetWindowConfig{
		{Window: "monthly", LimitUSD: 100},
		{Window: "daily", LimitUSD: 10, Mode: "hard"},
	}
	if res.Created || !reflect.DeepEqual(c.Budgets[0].Windows, want) || c.Budgets[0].Window != "" || c.Budgets[0].LimitUSD != 0 {
		t.Fatalf("budget = %+v; want windows %+v and nothing beside them", c.Budgets[0], want)
	}

	if _, err := c.UpsertBudget(BudgetUpdate{Name: "team", Window: "monthly", LimitUSD: 150}); err != nil {
		t.Fatalf("edit monthly: %v", err)
	}
	if got := c.Budgets[0].Windows[0].LimitUSD; got != 150 {
		t.Errorf("monthly limit = %v; want 150", got)
	}
	if _, err := c.UpsertBudget(BudgetUpdate{Name: "team", WarnAt: 0.5}); err != nil {
		t.Errorf("a budget-wide edit on a several-window budget: %v", err)
	}

	for _, tc := range []struct {
		name string
		u    BudgetUpdate
		want string
	}{
		{"limit without a window", BudgetUpdate{Name: "team", LimitUSD: 5}, "name the one"},
		{"unknown window", BudgetUpdate{Name: "team", Window: "weekly", LimitUSD: 5}, "no weekly window"},
		{"add existing", BudgetUpdate{Name: "team", Window: "daily", LimitUSD: 5, AddWindow: true}, "already has"},
		{"add without a window", BudgetUpdate{Name: "team", LimitUSD: 5, AddWindow: true}, "name the window"},
		{"add without a ceiling", BudgetUpdate{Name: "team", Window: "weekly", AddWindow: true}, "ceiling"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := c.Budgets[0]
			_, err := c.UpsertBudget(tc.u)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want one naming %q", err, tc.want)
			}
			if !reflect.DeepEqual(c.Budgets[0], before) {
				t.Errorf("rejected edit changed the budget: %+v", c.Budgets[0])
			}
		})
	}
}

// Going soft drops the window's enforcement; going hard again starts over
// in shadow, as every new boundary does.
func TestUpsertBudgetSoftClearsEnforcement(t *testing.T) {
	c := Default()
	c.Budgets = []BudgetConfig{{Name: "team", Window: "daily", LimitUSD: 10, Mode: "hard", Require: "deny", Stage: "enforce"}}
	if _, err := c.UpsertBudget(BudgetUpdate{Name: "team", Mode: "soft"}); err != nil {
		t.Fatal(err)
	}
	b := c.Budgets[0]
	if b.Mode != "" || b.Require != "" || b.Stage != "" {
		t.Fatalf("budget = %+v; want soft with no require or stage", b)
	}
	if _, err := c.UpsertBudget(BudgetUpdate{Name: "team", Mode: "hard"}); err != nil {
		t.Fatal(err)
	}
	if r := c.BoundaryRules(); len(r) != 1 || r[0].Enforced() {
		t.Errorf("rules = %+v; want one in shadow", r)
	}
}

// A single-window budget keeps its old meaning: naming another window
// moves it there.
func TestUpsertBudgetRetimesSingleWindow(t *testing.T) {
	c := Default()
	c.Budgets = []BudgetConfig{{Name: "m", Window: "monthly", LimitUSD: 50}}
	if _, err := c.UpsertBudget(BudgetUpdate{Name: "m", Window: "weekly"}); err != nil {
		t.Fatal(err)
	}
	if b := c.Budgets[0]; b.Window != "weekly" || b.LimitUSD != 50 || len(b.Windows) != 0 {
		t.Errorf("budget = %+v; want the one window moved to weekly", b)
	}
}

func TestRemoveBudgetWindow(t *testing.T) {
	c := Default()
	c.Budgets = []BudgetConfig{{Name: "team", Windows: []BudgetWindowConfig{
		{Window: "daily", LimitUSD: 10, Mode: "hard"},
		{Window: "monthly", LimitUSD: 100},
	}}}
	removed, err := c.RemoveBudgetWindow("team", "daily")
	if err != nil || !removed {
		t.Fatalf("RemoveBudgetWindow = %v, %v", removed, err)
	}
	// One window left: back to the single-window form.
	if b := c.Budgets[0]; b.Window != "monthly" || b.LimitUSD != 100 || len(b.Windows) != 0 {
		t.Errorf("budget = %+v; want the single-window form", b)
	}
	if _, err := c.RemoveBudgetWindow("team", "monthly"); err == nil {
		t.Error("removing the only window was accepted")
	}
	if removed, err := c.RemoveBudgetWindow("team", "weekly"); removed || err != nil {
		t.Errorf("missing window = %v, %v; want false, nil", removed, err)
	}
}

func TestSetBudgetRuleStage(t *testing.T) {
	c := Default()
	c.Budgets = []BudgetConfig{{Name: "team", Windows: []BudgetWindowConfig{
		{Window: "daily", LimitUSD: 10, Mode: "hard"},
		{Window: "monthly", LimitUSD: 100},
	}}}
	if !c.SetBudgetRuleStage("budget-team-daily", "enforce") {
		t.Fatal("the hard window's rule was not found")
	}
	if c.Budgets[0].Windows[0].Stage != "enforce" {
		t.Errorf("stage = %q", c.Budgets[0].Windows[0].Stage)
	}
	if c.SetBudgetRuleStage("budget-team-monthly", "enforce") {
		t.Error("a soft window has no rule to promote")
	}
}
