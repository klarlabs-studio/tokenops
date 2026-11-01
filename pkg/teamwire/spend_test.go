package teamwire

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// spendUpload is a valid schema 3 upload with a row in every section.
// The figures are invented.
func spendUpload() Upload {
	u := finopsUpload()
	u.Schema = SchemaSpend
	u.Spend = []SpendBucket{
		{Day: "2026-10-08", Repo: "klarlabs/tokenops", Provider: "anthropic", Turns: 4, Tokens: 1000, CostUSD: 0.3,
			APIEquivalentUSD: 1.2, UnpricedTurns: 1},
		{Day: "2026-10-08", Repo: "klarlabs/tokenops", Provider: "openai", Turns: 1, Tokens: 200, APIEquivalentUSD: 0.1},
	}
	u.Units = []UnitBucket{{Day: "2026-10-08", Repo: "klarlabs/tokenops", Tasks: 2, TaskUSD: 1.1, Commits: 3,
		CommitUSD: 0.9, MergedPRs: 1, MergedPRUSD: 0.7}}
	u.VendorCosts = []VendorCost{{Day: "2026-10-07", Provider: "anthropic", CostUSD: 41.5}}
	return u
}

func TestValidateSpend(t *testing.T) {
	if err := spendUpload().Validate(); err != nil {
		t.Fatalf("valid schema 3 upload rejected: %v", err)
	}
	// A unit with no work placed on it is a unit, not an error.
	u := spendUpload()
	u.Units[0].Commits, u.Units[0].CommitUSD = 2, 0
	if err := u.Validate(); err != nil {
		t.Errorf("commits without placed work rejected: %v", err)
	}
	cases := map[string]func(u *Upload){
		"sections in schema 2": func(u *Upload) { u.Schema = SchemaFinOps },
		"sections in schema 1": func(u *Upload) {
			u.Schema = SchemaBuckets
			u.PlanDays, u.Waste, u.Savings, u.PlanAdvice = nil, nil, nil, nil
		},
		"spend provider":           func(u *Upload) { u.Spend[0].Provider = "my own gateway" },
		"spend model as provider":  func(u *Upload) { u.Spend[0].Provider = "claude-opus-4-8" },
		"spend repo path":          func(u *Upload) { u.Spend[0].Repo = "/Users/alice/secret" },
		"spend negative tokens":    func(u *Upload) { u.Spend[0].Tokens = -1 },
		"spend unpriced > turns":   func(u *Upload) { u.Spend[0].UnpricedTurns = 5 },
		"spend negative cost":      func(u *Upload) { u.Spend[0].CostUSD = -0.1 },
		"spend NaN value":          func(u *Upload) { u.Spend[0].APIEquivalentUSD = math.NaN() },
		"spend day not covered":    func(u *Upload) { u.Spend[0].Day = "2026-10-01" },
		"duplicate spend":          func(u *Upload) { u.Spend[1] = u.Spend[0] },
		"units repo sentence":      func(u *Upload) { u.Units[0].Repo = "fix the login bug" },
		"units negative commits":   func(u *Upload) { u.Units[0].Commits = -1 },
		"value on no task":         func(u *Upload) { u.Units[0].Tasks = 0 },
		"value on no merged pr":    func(u *Upload) { u.Units[0].MergedPRs = 0 },
		"unbounded commit value":   func(u *Upload) { u.Units[0].CommitUSD = math.Inf(1) },
		"duplicate units":          func(u *Upload) { u.Units = append(u.Units, u.Units[0]) },
		"units day not covered":    func(u *Upload) { u.Units[0].Day = "2026-10-09" },
		"vendor provider":          func(u *Upload) { u.VendorCosts[0].Provider = "acme billing" },
		"vendor negative":          func(u *Upload) { u.VendorCosts[0].CostUSD = -1 },
		"vendor unbounded":         func(u *Upload) { u.VendorCosts[0].CostUSD = 1e12 },
		"duplicate vendor cost":    func(u *Upload) { u.VendorCosts = append(u.VendorCosts, u.VendorCosts[0]) },
		"vendor day not covered":   func(u *Upload) { u.VendorCosts[0].Day = "2026-10-06" },
		"too many vendor costs":    func(u *Upload) { u.VendorCosts = make([]VendorCost, MaxVendorCosts+1) },
		"too many spend rows":      func(u *Upload) { u.Spend = make([]SpendBucket, MaxSpend+1) },
		"schema beyond the newest": func(u *Upload) { u.Schema = SchemaVersion + 1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			u := spendUpload()
			mutate(&u)
			if err := u.Validate(); err == nil {
				t.Errorf("accepted %+v", u)
			}
		})
	}
}

// uploadV2 is the upload exactly as schema 2 servers know it.
type uploadV2 struct {
	uploadV1
	PlanDays   []PlanDay    `json:"plan_days,omitempty"`
	Waste      []Waste      `json:"waste,omitempty"`
	Savings    []SavingsDay `json:"savings,omitempty"`
	PlanAdvice []PlanAdvice `json:"plan_advice,omitempty"`
}

// A schema 2 server keeps working: what a client sends it has no key it
// does not know.
func TestSchema2UploadDecodesOnASchema2Server(t *testing.T) {
	sent := spendUpload().ForServer(SchemaFinOps)
	if sent.Schema != SchemaFinOps {
		t.Fatalf("schema = %d", sent.Schema)
	}
	if err := sent.Validate(); err != nil {
		t.Fatalf("schema 2 upload invalid: %v", err)
	}
	raw, err := json.Marshal(sent)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var old uploadV2
	if err := dec.Decode(&old); err != nil {
		t.Fatalf("a schema 2 server refuses it: %v\n%s", err, raw)
	}
	for _, k := range []string{"spend", "units", "vendor_cost"} {
		if strings.Contains(string(raw), `"`+k+`"`) {
			t.Errorf("schema 2 upload carries %q", k)
		}
	}
	if len(old.PlanDays) == 0 || len(old.PlanAdvice) == 0 {
		t.Error("the FinOps sections were dropped for a schema 2 server")
	}
}

func TestForServerSchema3(t *testing.T) {
	for _, c := range []struct {
		max, want      int
		finops, spends bool
	}{{0, 1, false, false}, {1, 1, false, false}, {2, 2, true, false}, {3, 3, true, true}, {4, 3, true, true}} {
		u := spendUpload().ForServer(c.max)
		if u.Schema != c.want || (len(u.PlanDays) > 0) != c.finops || (len(u.Spend) > 0) != c.spends ||
			(len(u.Units) > 0) != c.spends || (len(u.VendorCosts) > 0) != c.spends {
			t.Errorf("ForServer(%d) = schema %d, finops %v, spend %v", c.max, u.Schema, len(u.PlanDays) > 0, len(u.Spend) > 0)
		}
		if err := u.Validate(); err != nil {
			t.Errorf("ForServer(%d) invalid: %v", c.max, err)
		}
	}
	// A schema 2 upload to a schema 3 server stays schema 2.
	if u := finopsUpload().ForServer(SchemaSpend); u.Schema != SchemaFinOps {
		t.Errorf("schema 2 upload raised to %d", u.Schema)
	}
}

// The JSON field names are the wire contract; a rename breaks every
// deployed server.
func TestSpendJSONNames(t *testing.T) {
	u := spendUpload()
	for _, c := range []struct {
		v    any
		keys []string
	}{
		{u.Spend[0], []string{"day", "repo", "provider", "turns", "tokens", "cost_usd", "api_equivalent_usd", "unpriced_turns"}},
		{u.Units[0], []string{"day", "repo", "tasks", "task_usd", "commits", "commit_usd", "merged_prs", "merged_pr_usd"}},
		{u.VendorCosts[0], []string{"day", "provider", "cost_usd"}},
	} {
		raw, err := json.Marshal(c.v)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range c.keys {
			if _, ok := m[k]; !ok {
				t.Errorf("%T JSON lacks %q", c.v, k)
			}
			delete(m, k)
		}
		if len(m) > 0 {
			t.Errorf("unexpected %T fields: %v", c.v, m)
		}
	}
	raw, _ := json.Marshal(u)
	for _, k := range []string{`"spend":`, `"units":`, `"vendor_cost":`} {
		if !strings.Contains(string(raw), k) {
			t.Errorf("upload JSON lacks %s", k)
		}
	}
}
