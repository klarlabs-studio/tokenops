package dedupe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/felixgeelhaar/tokenops/internal/optimizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func longText(seed string, repeats int) string {
	return strings.Repeat(seed+" ", repeats)
}

func mustBody(t *testing.T, msgs []map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"model":    "gpt-4o-mini",
		"messages": msgs,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

// extractMessages decodes a body and returns its message contents in
// order, exposing only the role + content text so tests can assert
// rewrites without re-parsing JSON each time.
func extractMessages(t *testing.T, body []byte) []struct{ Role, Content string } {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal(raw["messages"], &arr); err != nil {
		t.Fatalf("unmarshal messages: %v", err)
	}
	out := make([]struct{ Role, Content string }, len(arr))
	for i, m := range arr {
		var r, c string
		if rb, ok := m["role"]; ok {
			_ = json.Unmarshal(rb, &r)
		}
		if cb, ok := m["content"]; ok {
			// content might be string or array-of-parts; tests build
			// strings, so a string decode covers them.
			_ = json.Unmarshal(cb, &c)
		}
		out[i] = struct{ Role, Content string }{Role: r, Content: c}
	}
	return out
}

func TestDeduperFindsExactDuplicate(t *testing.T) {
	repeated := longText("the quick brown fox jumps over the lazy dog", 8)
	msgs := []map[string]any{
		{"role": "user", "content": "Summarise the document below."},
		{"role": "assistant", "content": repeated},
		{"role": "user", "content": "Now translate the same text:"},
		{"role": "assistant", "content": repeated},
	}
	body := mustBody(t, msgs)

	d := New(Config{}, nil)
	recs, err := d.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI,
		Body:     body,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("recs=%d, want 1", len(recs))
	}
	if recs[0].Kind != eventschema.OptimizationTypeDedupe {
		t.Errorf("kind = %q", recs[0].Kind)
	}
	if recs[0].EstimatedSavingsTokens <= 0 {
		t.Errorf("savings = %d", recs[0].EstimatedSavingsTokens)
	}

	got := extractMessages(t, recs[0].ApplyBody)
	if len(got) != 4 {
		t.Fatalf("messages = %d", len(got))
	}
	if !strings.HasPrefix(got[3].Content, "[duplicate of message #2") {
		t.Errorf("expected pointer placeholder at index 3, got %q", got[3].Content)
	}
	if got[1].Content != repeated {
		t.Errorf("first occurrence must remain unchanged")
	}
}

func TestDeduperFindsNearDuplicate(t *testing.T) {
	original := longText("retrieve all rows from the orders table where status equals shipped", 6)
	// Slight phrasing change but same shingles dominate.
	near := original + " (minor edit)"
	msgs := []map[string]any{
		{"role": "user", "content": "Plan the query."},
		{"role": "assistant", "content": original},
		{"role": "user", "content": "Plan the query again."},
		{"role": "assistant", "content": near},
	}
	body := mustBody(t, msgs)

	d := New(Config{SimilarityThreshold: 0.7}, nil)
	recs, err := d.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic,
		Body:     body,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("recs=%d, want 1", len(recs))
	}
}

func TestDeduperIgnoresSystemRoleByDefault(t *testing.T) {
	repeated := longText("you are a helpful assistant who follows instructions carefully", 10)
	msgs := []map[string]any{
		{"role": "system", "content": repeated},
		{"role": "user", "content": "Hi."},
		{"role": "system", "content": repeated},
		{"role": "user", "content": "Anything?"},
	}
	body := mustBody(t, msgs)

	d := New(Config{}, nil)
	recs, err := d.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI,
		Body:     body,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("expected no recommendation when system roles guarded, got %d", len(recs))
	}
}

func TestDeduperDropSystemRoleWhenEnabled(t *testing.T) {
	repeated := longText("you are a helpful assistant who follows instructions carefully", 10)
	msgs := []map[string]any{
		{"role": "system", "content": repeated},
		{"role": "user", "content": "Hi."},
		{"role": "system", "content": repeated},
	}
	body := mustBody(t, msgs)

	d := New(Config{DropSystemRoles: true}, nil)
	recs, err := d.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI,
		Body:     body,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("recs=%d, want 1", len(recs))
	}
}

func TestDeduperRespectsMinMessageBytes(t *testing.T) {
	short := "hi there"
	msgs := []map[string]any{
		{"role": "user", "content": short},
		{"role": "user", "content": short},
		{"role": "user", "content": short},
	}
	body := mustBody(t, msgs)

	d := New(Config{}, nil)
	recs, _ := d.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI,
		Body:     body,
	})
	if len(recs) != 0 {
		t.Errorf("short messages should not trigger dedupe: %d recs", len(recs))
	}
}

func TestDeduperRespectsMinSavingsTokens(t *testing.T) {
	// Two messages each just over MinMessageBytes, but configured savings
	// floor exceeds what one cluster yields.
	repeated := longText("alpha beta gamma delta epsilon zeta eta theta", 4)
	msgs := []map[string]any{
		{"role": "user", "content": repeated},
		{"role": "user", "content": repeated},
	}
	body := mustBody(t, msgs)

	d := New(Config{MinSavingsTokens: 1_000_000}, nil)
	recs, _ := d.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI,
		Body:     body,
	})
	if len(recs) != 0 {
		t.Errorf("savings floor should suppress recommendation: %d recs", len(recs))
	}
}

func TestDeduperPreservesNonDuplicates(t *testing.T) {
	a := longText("alpha beta gamma delta epsilon zeta eta theta iota kappa", 4)
	b := longText("the quick brown fox jumps over the lazy dog", 6)
	msgs := []map[string]any{
		{"role": "user", "content": a},
		{"role": "user", "content": b},
	}
	body := mustBody(t, msgs)

	d := New(Config{}, nil)
	recs, _ := d.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI,
		Body:     body,
	})
	if len(recs) != 0 {
		t.Errorf("non-duplicate corpus should not trigger dedupe: %d recs", len(recs))
	}
}

func TestDeduperUnsupportedProvider(t *testing.T) {
	d := New(Config{}, nil)
	recs, _ := d.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderGemini,
		Body:     []byte(`{"contents":[]}`),
	})
	if len(recs) != 0 {
		t.Errorf("gemini not yet supported, expected no recommendation, got %d", len(recs))
	}
}

func TestDeduperHandlesArrayOfParts(t *testing.T) {
	repeated := longText("retrieve all the documents matching the query and summarise them", 6)
	msgs := []map[string]any{
		{"role": "user", "content": []map[string]any{
			{"type": "text", "text": repeated},
		}},
		{"role": "user", "content": []map[string]any{
			{"type": "text", "text": repeated},
		}},
	}
	body := mustBody(t, msgs)

	d := New(Config{}, nil)
	recs, err := d.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderAnthropic,
		Body:     body,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("recs=%d", len(recs))
	}
}

func TestPipelineIntegratesDedupe(t *testing.T) {
	repeated := longText("the quick brown fox jumps over the lazy dog", 8)
	msgs := []map[string]any{
		{"role": "user", "content": repeated},
		{"role": "assistant", "content": repeated},
		{"role": "user", "content": repeated},
	}
	body := mustBody(t, msgs)

	d := New(Config{}, nil)
	pipe := optimizer.NewPipeline(d)
	res, err := pipe.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI,
		Body:     body,
		Mode:     optimizer.ModePassive,
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("events=%d", len(res.Events))
	}
	if res.Events[0].Kind != eventschema.OptimizationTypeDedupe {
		t.Errorf("kind=%s", res.Events[0].Kind)
	}
	if res.Events[0].EstimatedSavingsTokens <= 0 {
		t.Errorf("savings=%d", res.Events[0].EstimatedSavingsTokens)
	}
	// Passive mode: body unchanged.
	if string(res.Body) != string(body) {
		t.Errorf("passive mode must not mutate body")
	}
}

func TestDeduperShinglesSimilarityScoring(t *testing.T) {
	// Sanity check: identical text → 1.0; disjoint → 0; subset → between.
	a := shingles("the quick brown fox jumps over the lazy dog")
	b := shingles("the quick brown fox jumps over the lazy dog")
	c := shingles("alpha beta gamma delta epsilon zeta eta theta")
	if got := jaccard(a, b); got != 1 {
		t.Errorf("identical jaccard = %v, want 1", got)
	}
	if got := jaccard(a, c); got != 0 {
		t.Errorf("disjoint jaccard = %v, want 0", got)
	}
}

// dummy use of fmt to avoid an unused-import flake in case of future edits.
var _ = fmt.Sprintf
