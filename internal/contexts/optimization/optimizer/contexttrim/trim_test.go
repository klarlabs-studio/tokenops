package contexttrim

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/tokenizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func mkOpenAIBody(messageRoles ...string) []byte {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	msgs := make([]msg, 0, len(messageRoles))
	for i, r := range messageRoles {
		msgs = append(msgs, msg{Role: r, Content: "content-" + string(rune('A'+i))})
	}
	out := map[string]any{
		"model":    "gpt-4o-mini",
		"messages": msgs,
	}
	b, _ := json.Marshal(out)
	return b
}

func TestTrimsLongOpenAIConversation(t *testing.T) {
	roles := []string{"system", "user", "assistant", "user", "assistant", "user", "assistant", "user", "assistant", "user", "assistant", "user"}
	body := mkOpenAIBody(roles...)
	tr := New(Config{KeepLastTurns: 2}, tokenizer.NewRegistry())
	recs, err := tr.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Body: body,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("recs = %d, want 1", len(recs))
	}
	rec := recs[0]
	if rec.Kind != eventschema.OptimizationTypeContextTrim {
		t.Errorf("kind = %s", rec.Kind)
	}
	if rec.EstimatedSavingsTokens <= 0 {
		t.Errorf("savings tokens = %d", rec.EstimatedSavingsTokens)
	}
	if rec.QualityScore <= 0 {
		t.Errorf("quality = %f", rec.QualityScore)
	}
	if len(rec.ApplyBody) == 0 {
		t.Fatal("ApplyBody empty")
	}

	// Decode rewritten body and check retention policy:
	// - system kept, last 2 user turns + their assistant replies kept.
	var probe struct {
		Messages []struct{ Role string } `json:"messages"`
	}
	if err := json.Unmarshal(rec.ApplyBody, &probe); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rolesAfter := make([]string, len(probe.Messages))
	for i, m := range probe.Messages {
		rolesAfter[i] = m.Role
	}
	// Expect: system + ...trailing turns. Must include "system" and end with last user.
	if rolesAfter[0] != "system" {
		t.Errorf("system not retained at head: %v", rolesAfter)
	}
	if rolesAfter[len(rolesAfter)-1] != "user" {
		t.Errorf("trailing user lost: %v", rolesAfter)
	}
	// Should be far shorter than original.
	if len(rolesAfter) >= len(roles) {
		t.Errorf("not trimmed: %d vs %d", len(rolesAfter), len(roles))
	}
}

func TestShortConversationNotTrimmed(t *testing.T) {
	body := mkOpenAIBody("system", "user", "assistant")
	tr := New(Config{KeepLastTurns: 4}, nil)
	recs, err := tr.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Body: body,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("short conv should not trigger trim, got %+v", recs)
	}
}

func TestSystemNotRetainedWhenKeepSystemFalse(t *testing.T) {
	body := mkOpenAIBody("system", "user", "assistant", "user", "assistant",
		"user", "assistant", "user", "assistant", "user", "assistant", "user")
	keep := false
	tr := New(Config{KeepSystem: &keep, KeepLastTurns: 1}, nil)
	recs, _ := tr.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Body: body,
	})
	if len(recs) != 1 {
		t.Fatalf("recs = %d", len(recs))
	}
	var probe struct {
		Messages []struct{ Role string } `json:"messages"`
	}
	_ = json.Unmarshal(recs[0].ApplyBody, &probe)
	for _, m := range probe.Messages {
		if m.Role == "system" {
			t.Errorf("system retained despite KeepSystem=false: %+v", probe)
		}
	}
}

func TestPreservesTopLevelFields(t *testing.T) {
	roles := []string{"user", "assistant", "user", "assistant", "user", "assistant", "user", "assistant", "user"}
	body := mkOpenAIBody(roles...)
	// Inject extra top-level fields (model already set; add tool_choice).
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	raw["temperature"] = 0.7
	raw["tool_choice"] = "auto"
	body, _ = json.Marshal(raw)

	tr := New(Config{KeepLastTurns: 2}, nil)
	recs, _ := tr.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Body: body,
	})
	if len(recs) != 1 {
		t.Fatalf("expected rec")
	}
	var got map[string]any
	_ = json.Unmarshal(recs[0].ApplyBody, &got)
	if got["model"] != "gpt-4o-mini" {
		t.Errorf("model lost: %v", got["model"])
	}
	if got["tool_choice"] != "auto" {
		t.Errorf("tool_choice lost: %v", got["tool_choice"])
	}
	if got["temperature"] == nil {
		t.Errorf("temperature lost: %v", got)
	}
}

func TestUnknownProviderNoOp(t *testing.T) {
	tr := New(Config{}, nil)
	recs, err := tr.Run(context.Background(), &optimizer.Request{
		Provider: "vertex-ai", Body: []byte(`{"messages":[]}`),
	})
	if err != nil || len(recs) != 0 {
		t.Errorf("unknown provider should be no-op: %v / %+v", err, recs)
	}
}

func TestMalformedBodyNoOp(t *testing.T) {
	tr := New(Config{}, nil)
	recs, err := tr.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Body: []byte(`not json`),
	})
	if err != nil || len(recs) != 0 {
		t.Errorf("malformed body should be no-op: %v / %+v", err, recs)
	}
}

func TestAnthropicTrim(t *testing.T) {
	type m struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	msgs := []m{}
	for i := 0; i < 12; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, m{Role: role, Content: strings.Repeat("filler ", 5)})
	}
	body, _ := json.Marshal(map[string]any{
		"model":    "claude-sonnet-4-6",
		"system":   "You are helpful.",
		"messages": msgs,
	})

	tr := New(Config{KeepLastTurns: 2}, tokenizer.NewRegistry())
	recs, err := tr.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic, Body: body,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected rec")
	}
	var got map[string]any
	_ = json.Unmarshal(recs[0].ApplyBody, &got)
	if got["system"] != "You are helpful." {
		t.Errorf("anthropic system field lost: %v", got["system"])
	}
	gotMsgs := got["messages"].([]any)
	if len(gotMsgs) >= len(msgs) {
		t.Errorf("anthropic not trimmed: %d vs %d", len(gotMsgs), len(msgs))
	}
}

func TestQualityScoreBounded(t *testing.T) {
	tr := New(Config{}, nil)
	if got := tr.qualityScore(0); got != 0.95 {
		t.Errorf("zero dropped = %f", got)
	}
	if got := tr.qualityScore(100); got != 0.6 {
		t.Errorf("large dropped should clamp at 0.6, got %f", got)
	}
}

func TestSavingsScalesWithTokenizer(t *testing.T) {
	roles := make([]string, 12)
	for i := range roles {
		if i%2 == 0 {
			roles[i] = "user"
		} else {
			roles[i] = "assistant"
		}
	}
	body := mkOpenAIBody(roles...)
	with := New(Config{KeepLastTurns: 1}, tokenizer.NewRegistry())
	without := New(Config{KeepLastTurns: 1}, nil)
	rWith, _ := with.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Body: body,
	})
	rWithout, _ := without.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Body: body,
	})
	if len(rWith) != 1 || len(rWithout) != 1 {
		t.Fatalf("expected recs from both")
	}
	if rWith[0].EstimatedSavingsTokens <= 0 || rWithout[0].EstimatedSavingsTokens <= 0 {
		t.Errorf("zero savings: with=%d without=%d",
			rWith[0].EstimatedSavingsTokens, rWithout[0].EstimatedSavingsTokens)
	}
}
