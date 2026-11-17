package teamwire

import (
	"encoding/json"
	"strings"
	"testing"
)

// modelUpload is a valid schema 6 upload whose spend rows are split by
// model, agent and effort: an Anthropic row over two catalog models, two
// agents and two effort levels, and an OpenAI row on a model the catalog
// does not price with no effort recorded. The figures are invented.
func modelUpload() Upload {
	u := seatUpload()
	u.Spend = []SpendBucket{
		{Day: "2026-11-12", Repo: "acme/api", Provider: "anthropic", Turns: 5, Tokens: 1500, CostUSD: 0.3,
			APIEquivalentUSD: 1.3, UnpricedTurns: 1},
		{Day: "2026-11-13", Repo: RepoNone, Provider: "openai", Turns: 2, Tokens: 300},
	}
	u.SpendModels = []ModelSpend{
		{Day: "2026-11-12", Repo: "acme/api", Provider: "anthropic", Model: "claude-opus-4-8", Agent: "claude-code",
			Effort: "high", Turns: 2, Tokens: 700, CostUSD: 0.06, APIEquivalentUSD: 0.7},
		{Day: "2026-11-12", Repo: "acme/api", Provider: "anthropic", Model: "claude-opus-4-8", Agent: "claude-code",
			Effort: "low", Turns: 1, Tokens: 300, CostUSD: 0.04, APIEquivalentUSD: 0.3},
		{Day: "2026-11-12", Repo: "acme/api", Provider: "anthropic", Model: "claude-opus-4-8", Agent: "opencode",
			Effort: EffortOther, Turns: 1, Tokens: 300, CostUSD: 0.2, APIEquivalentUSD: 0.3},
		{Day: "2026-11-12", Repo: "acme/api", Provider: "anthropic", Model: ModelOther, Agent: AgentOther,
			Effort: EffortUnknown, Turns: 1, Tokens: 200, UnpricedTurns: 1},
		{Day: "2026-11-13", Repo: RepoNone, Provider: "openai", Model: ModelOther, Agent: "codex", Effort: EffortUnknown,
			Turns: 2, Tokens: 300},
	}
	return u
}

func TestModelSpendValidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Upload)
		want   string
	}{
		{"valid", func(*Upload) {}, ""},
		{"no split", func(u *Upload) { u.SpendModels = nil }, ""},
		{"schema 5 with a split", func(u *Upload) {
			u.Schema = SchemaPrevented
			u.PlanDays, u.PlanAdvice, u.PlanBindings, u.PlanWindows = nil, nil, nil, nil
		}, "carries no spend by model"},
		{"a model the catalog does not price", func(u *Upload) { u.SpendModels[0].Model = "acme-finetune-v2" }, "not a catalog model"},
		{"a dated ID, not the catalog's", func(u *Upload) { u.SpendModels[0].Model = "claude-opus-4-8-20261001" }, "not a catalog model"},
		{"another provider's model", func(u *Upload) { u.SpendModels[0].Model = "gpt-4o" }, "not a catalog model"},
		{"a sentence as a model", func(u *Upload) { u.SpendModels[0].Model = "fix the login bug" }, "not a catalog model"},
		{"an empty model", func(u *Upload) { u.SpendModels[0].Model = "" }, "not a catalog model"},
		{"an unknown agent", func(u *Upload) { u.SpendModels[0].Agent = "my-editor" }, "not an agent"},
		{"an unknown provider", func(u *Upload) { u.SpendModels[4].Provider = "acme" }, "provider"},
		{"a path as a repo", func(u *Upload) { u.SpendModels[0].Repo = "/Users/alice/x" }, "repo"},
		{"a day not covered", func(u *Upload) { u.SpendModels[4].Day = "2026-11-01" }, "not in days"},
		{"negative tokens", func(u *Upload) { u.SpendModels[0].Tokens = -1 }, "negative"},
		{"unpriced past turns", func(u *Upload) { u.SpendModels[3].UnpricedTurns = 2 }, "unpriced_turns"},
		{"an effort outside the set", func(u *Upload) { u.SpendModels[0].Effort = "turbo" }, "not an effort"},
		{"a client's raw label", func(u *Upload) { u.SpendModels[0].Effort = "High " }, "not an effort"},
		{"no effort", func(u *Upload) { u.SpendModels[0].Effort = "" }, "not an effort"},
		{"a row twice", func(u *Upload) { u.SpendModels[1].Effort = "high" }, "appears twice"},
		{"too many rows", func(u *Upload) {
			u.SpendModels = make([]ModelSpend, MaxModelSpend+1)
		}, "at most"},
		{"rows that do not add up", func(u *Upload) { u.SpendModels[0].Turns = 4 }, "do not add up"},
		{"dollars that do not add up", func(u *Upload) { u.SpendModels[0].APIEquivalentUSD = 1.01 }, "do not add up"},
		{"a spend row left unsplit", func(u *Upload) { u.SpendModels = u.SpendModels[:4] }, "no rows for spend"},
		{"a split with no spend row", func(u *Upload) { u.Spend = u.Spend[:1] }, "has no spend row"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := modelUpload()
			tt.change(&u)
			err := u.Validate()
			if tt.want == "" {
				if err != nil {
					t.Errorf("valid upload refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v; want %q", err, tt.want)
			}
		})
	}
}

// A model crosses as the catalog's ID, and anything the catalog does not
// price as "other".
func TestCatalogModelForUpload(t *testing.T) {
	for _, c := range []struct{ provider, model, want string }{
		{"anthropic", "claude-opus-4-8-20261001", "claude-opus-4-8"},
		{"openai", "gpt-4o-2024-08-06", "gpt-4o"},
		{"github", "claude-opus-4.8", "claude-opus-4-8"},
		{"anthropic", "acme-finetune-v2", ModelOther},
		{"openai", "ft:gpt-4o-mini:acme:secret-project:abc123", ModelOther},
		{"ollama", "llama3:8b", ModelOther},
		{"anthropic", "", ModelOther},
	} {
		if got := CatalogModel(c.provider, c.model); got != c.want {
			t.Errorf("CatalogModel(%s, %q) = %q; want %q", c.provider, c.model, got, c.want)
		}
		if !ValidModel(c.provider, CatalogModel(c.provider, c.model)) {
			t.Errorf("CatalogModel(%s, %q) is not a valid model", c.provider, c.model)
		}
	}
	if ValidModel("acme", ModelOther) {
		t.Error("ValidModel accepts an unregistered provider")
	}
}

// Fine-tunes are named by their owner ("ft:gpt-4o-mini:acme:..."); the
// prefix rows would price them as the base model, so check the ID that
// crosses carries nothing of the fine-tune's name.
func TestFineTuneCrossesWithoutItsName(t *testing.T) {
	got := CatalogModel("openai", "ft:gpt-4o-mini:acme:secret-project:abc123")
	if strings.Contains(got, "acme") || strings.Contains(got, "secret") {
		t.Errorf("fine-tune crosses as %q", got)
	}
}

func TestModelSpendJSONNames(t *testing.T) {
	b, err := jsonOf(modelUpload().SpendModels[0])
	if err != nil {
		t.Fatal(err)
	}
	want := `{"day":"2026-11-12","repo":"acme/api","provider":"anthropic","model":"claude-opus-4-8","agent":"claude-code",` +
		`"effort":"high","turns":2,"tokens":700,"cost_usd":0.06,"api_equivalent_usd":0.7,"unpriced_turns":0}`
	if b != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

// A server below schema 6 gets no split, and no key it does not know.
func TestForServerDropsSpendModelsBelowSchema6(t *testing.T) {
	full := modelUpload()
	u := full.ForServer(SchemaPrevented)
	if u.SpendModels != nil || len(u.Spend) != 2 {
		t.Fatalf("for a schema 5 server: %+v", u)
	}
	b, _ := json.Marshal(u)
	if strings.Contains(string(b), `"spend_models"`) {
		t.Errorf("a schema 5 upload carries spend_models: %s", b)
	}
	if err := u.Validate(); err != nil {
		t.Errorf("the schema 5 upload is invalid: %v", err)
	}
	if full.SpendModels == nil {
		t.Error("ForServer changed the upload it was given")
	}
	if got := full.ForServer(SchemaSeats); len(got.SpendModels) != 5 {
		t.Errorf("a schema 6 server gets the split whole: %+v", got.SpendModels)
	}
}

// Each client's raw effort label crosses only as a level of the closed
// set, "other" for a label outside it, or "unknown" when none was
// recorded.
func TestEffortLevel(t *testing.T) {
	for _, c := range []struct{ raw, want string }{
		{"none", "none"},
		{"minimal", "minimal"},
		{"low", "low"},
		{"medium", "medium"},
		{"high", "high"},
		{"xhigh", "xhigh"},
		{"max", "max"},
		{"High", "high"},       // Claude Code and opencode labels, as typed
		{" MEDIUM ", "medium"}, // Codex's turn_context, padded
		{"x-high", "xhigh"},
		{"extra-high", "xhigh"},
		{"extra_high", "xhigh"},
		{"maximum", "max"},
		{"med", "medium"},
		{"turbo", EffortOther},             // an opencode variant of the operator's own naming
		{"my secret project", EffortOther}, // free text never crosses
		{"ultrathink", EffortOther},
		{"", EffortUnknown},
		{"   ", EffortUnknown},
	} {
		got := EffortLevel(c.raw)
		if got != c.want {
			t.Errorf("EffortLevel(%q) = %q; want %q", c.raw, got, c.want)
		}
		if !ValidEffort(got) {
			t.Errorf("EffortLevel(%q) = %q is not a valid effort", c.raw, got)
		}
	}
	for _, e := range Efforts {
		if EffortLevel(e) != e {
			t.Errorf("EffortLevel(%q) = %q; every level is its own normal form", e, EffortLevel(e))
		}
	}
	if ValidEffort("") || ValidEffort("High") {
		t.Error("ValidEffort accepts a label outside the set")
	}
}
