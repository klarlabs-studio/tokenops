package teamwire

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func pct(v float64) *float64 { return &v }

// finopsUpload is a valid schema 2 upload with a row in every section.
// The figures are invented.
func finopsUpload() Upload {
	u := validUpload()
	u.Schema = SchemaFinOps
	u.PlanDays = []PlanDay{{Day: "2026-10-08", Provider: "anthropic", Plan: "claude-max", UtilizationPct: pct(42.5),
		UtilizationWindows: 1, CoveragePct: pct(80), PlanTokens: 800, BilledTokens: 200, Lockouts: 1, BlockedMinutes: 35}}
	u.Waste = []Waste{{Day: "2026-10-08", Repo: "klarlabs/tokenops", Kind: KindEdit, Class: WasteReread,
		Turns: 2, Tokens: 300, APIEquivalentUSD: 0.12}}
	u.Savings = []SavingsDay{{Day: "2026-10-07", Interventions: 3, Held: 1, RefusedRereads: 2, RefusedTokens: 900,
		RefusedUSD: 0.3, HoldoutUSD: pct(0.5), RealizedUSD: 0.8, AvoidableUSD: 1.1}}
	u.PlanAdvice = []PlanAdvice{{Provider: "anthropic", Plan: "claude-ultra", Verdict: "switch", Kind: "plan",
		RecommendedPlan: "claude-max", MonthlyDeltaUSD: 100, Material: true, RecommendedLockouts: 1,
		RecommendedBlockedMinutes: 40, Confidence: "medium", Days: 30, WindowsMeasured: 12}}
	return u
}

func TestValidateFinOps(t *testing.T) {
	if err := finopsUpload().Validate(); err != nil {
		t.Fatalf("valid schema 2 upload rejected: %v", err)
	}
	cases := map[string]func(u *Upload){
		"sections in schema 1":      func(u *Upload) { u.Schema = SchemaBuckets },
		"unknown provider":          func(u *Upload) { u.PlanDays[0].Provider = "acme ai" },
		"plan of another provider":  func(u *Upload) { u.PlanDays[0].Plan = "gpt-plus" },
		"unknown plan":              func(u *Upload) { u.PlanDays[0].Plan = "my private plan" },
		"utilization over 100":      func(u *Upload) { u.PlanDays[0].UtilizationPct = pct(101) },
		"negative coverage":         func(u *Upload) { u.PlanDays[0].CoveragePct = pct(-1) },
		"NaN utilization":           func(u *Upload) { u.PlanDays[0].UtilizationPct = pct(math.NaN()) },
		"negative lockouts":         func(u *Upload) { u.PlanDays[0].Lockouts = -1 },
		"blocked over a day":        func(u *Upload) { u.PlanDays[0].BlockedMinutes = 24*60 + 1 },
		"plan day not covered":      func(u *Upload) { u.PlanDays[0].Day = "2026-10-01" },
		"duplicate plan day":        func(u *Upload) { u.PlanDays = append(u.PlanDays, u.PlanDays[0]) },
		"waste class":               func(u *Upload) { u.Waste[0].Class = "too many tokens on the parser" },
		"waste repo path":           func(u *Upload) { u.Waste[0].Repo = "/Users/alice/secret" },
		"waste kind":                func(u *Upload) { u.Waste[0].Kind = "refactor" },
		"negative waste tokens":     func(u *Upload) { u.Waste[0].Tokens = -5 },
		"negative waste dollars":    func(u *Upload) { u.Waste[0].APIEquivalentUSD = -0.1 },
		"infinite waste dollars":    func(u *Upload) { u.Waste[0].APIEquivalentUSD = math.Inf(1) },
		"waste day not covered":     func(u *Upload) { u.Waste[0].Day = "2026-09-30" },
		"duplicate waste":           func(u *Upload) { u.Waste = append(u.Waste, u.Waste[0]) },
		"unbounded realized":        func(u *Upload) { u.Savings[0].RealizedUSD = math.Inf(1) },
		"negative avoidable":        func(u *Upload) { u.Savings[0].AvoidableUSD = -1 },
		"negative interventions":    func(u *Upload) { u.Savings[0].Interventions = -1 },
		"unbounded holdout":         func(u *Upload) { u.Savings[0].HoldoutUSD = pct(1e12) },
		"duplicate savings day":     func(u *Upload) { u.Savings = append(u.Savings, u.Savings[0]) },
		"savings day not covered":   func(u *Upload) { u.Savings[0].Day = "2026-10-09" },
		"verdict":                   func(u *Upload) { u.PlanAdvice[0].Verdict = "maybe downgrade" },
		"option kind":               func(u *Upload) { u.PlanAdvice[0].Kind = "barter" },
		"confidence":                func(u *Upload) { u.PlanAdvice[0].Confidence = "certain" },
		"recommended unknown":       func(u *Upload) { u.PlanAdvice[0].RecommendedPlan = "a cheaper one" },
		"api names a plan":          func(u *Upload) { u.PlanAdvice[0].Kind = "api" },
		"advice days":               func(u *Upload) { u.PlanAdvice[0].Days = 400 },
		"negative advice lockouts":  func(u *Upload) { u.PlanAdvice[0].RecommendedLockouts = -1 },
		"unbounded delta":           func(u *Upload) { u.PlanAdvice[0].MonthlyDeltaUSD = math.Inf(-1) },
		"duplicate provider advice": func(u *Upload) { u.PlanAdvice = append(u.PlanAdvice, u.PlanAdvice[0]) },
		"advice for unknown provider": func(u *Upload) {
			u.PlanAdvice[0].Provider = "nobody"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			u := finopsUpload()
			mutate(&u)
			if err := u.Validate(); err == nil {
				t.Errorf("accepted %+v", u)
			}
		})
	}
	// An upgrade costs more: a negative delta is a figure, not an error;
	// and the API option names no plan.
	u := finopsUpload()
	u.PlanAdvice[0].Verdict, u.PlanAdvice[0].MonthlyDeltaUSD = "upgrade", -100
	u.PlanAdvice = append(u.PlanAdvice, PlanAdvice{Provider: "openai", Plan: "subscription", Verdict: "switch",
		Kind: "api", Confidence: "low", Days: 10})
	if err := u.Validate(); err != nil {
		t.Errorf("upgrade and API advice rejected: %v", err)
	}
}

// uploadV1 is the upload exactly as schema 1 servers know it: what an
// older server decodes into, with unknown fields refused.
type uploadV1 struct {
	Schema        int       `json:"schema"`
	BatchID       string    `json:"batch_id"`
	ComputedAt    time.Time `json:"computed_at"`
	ClientVersion string    `json:"client_version"`
	Days          []string  `json:"days"`
	Buckets       []Bucket  `json:"buckets"`
}

// A schema 1 server keeps working: what a client sends it has no key it
// does not know, and reads as before.
func TestSchema1UploadDecodesOnAnOldServer(t *testing.T) {
	sent := finopsUpload().ForServer(0)
	if sent.Schema != SchemaBuckets {
		t.Fatalf("schema = %d", sent.Schema)
	}
	if err := sent.Validate(); err != nil {
		t.Fatalf("schema 1 upload invalid: %v", err)
	}
	raw, err := json.Marshal(sent)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var old uploadV1
	if err := dec.Decode(&old); err != nil {
		t.Fatalf("a schema 1 server refuses it: %v\n%s", err, raw)
	}
	for _, k := range []string{"plan_days", "waste", "savings", "plan_advice"} {
		if strings.Contains(string(raw), `"`+k+`"`) {
			t.Errorf("schema 1 upload carries %q", k)
		}
	}
	// Byte for byte what the schema 1 shape encodes to.
	want, _ := json.Marshal(old)
	if !bytes.Equal(raw, want) {
		t.Errorf("schema 1 upload differs from the schema 1 shape:\n got %s\nwant %s", raw, want)
	}
}

func TestForServer(t *testing.T) {
	for _, c := range []struct {
		max, want int
		sections  bool
	}{{0, 1, false}, {1, 1, false}, {2, 2, true}, {3, 2, true}} {
		u := finopsUpload().ForServer(c.max)
		if u.Schema != c.want || (len(u.PlanDays) > 0) != c.sections || (len(u.PlanAdvice) > 0) != c.sections {
			t.Errorf("ForServer(%d) = schema %d, sections %v", c.max, u.Schema, len(u.PlanDays) > 0)
		}
		if err := u.Validate(); err != nil {
			t.Errorf("ForServer(%d) invalid: %v", c.max, err)
		}
	}
}

// Me's max_schema is optional: a server that predates it reads as schema 1.
func TestMeMaxSchemaOptional(t *testing.T) {
	var me Me
	if err := json.Unmarshal([]byte(`{"org_name":"Example Org"}`), &me); err != nil || me.MaxSchema != 0 {
		t.Fatalf("me %+v err %v", me, err)
	}
	raw, _ := json.Marshal(Me{})
	if strings.Contains(string(raw), "max_schema") {
		t.Errorf("zero max_schema encoded: %s", raw)
	}
}

// An older client still sends the names Anthropic retired: the server
// accepts them and files them under the plan's current name.
func TestRetiredPlanNames(t *testing.T) {
	for _, plan := range []string{"claude-max-20x", "claude-max-5x", "claude-ultra", "claude-max"} {
		if !ValidPlan("anthropic", plan) {
			t.Errorf("ValidPlan(anthropic, %q) = false", plan)
		}
	}
	if ValidPlan("openai", "claude-max-20x") {
		t.Error("a retired Anthropic name is valid for another provider")
	}
	for old, want := range map[string]string{"claude-max-20x": "claude-ultra", "claude-max-5x": "claude-max",
		"claude-max": "claude-max", "gpt-plus": "gpt-plus", "pay-as-you-go": "pay-as-you-go"} {
		if got := CurrentPlan(old); got != want {
			t.Errorf("CurrentPlan(%q) = %q, want %q", old, got, want)
		}
	}
}
