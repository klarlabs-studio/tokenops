package promptcompress

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/felixgeelhaar/tokenops/internal/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/tokenizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func TestCollapsesWhitespace(t *testing.T) {
	in := "hello    world\n\n\n\n  next line   "
	out := compressString(in)
	if strings.Contains(out, "    ") {
		t.Errorf("multiple spaces lingering: %q", out)
	}
	if strings.Contains(out, "\n\n\n") {
		t.Errorf("excess blank lines lingering: %q", out)
	}
	if strings.HasSuffix(out, "   ") {
		t.Errorf("trailing whitespace lingering: %q", out)
	}
}

func TestStripsHTMLComments(t *testing.T) {
	in := "before <!-- secret note --> after"
	out := compressString(in)
	if strings.Contains(out, "secret") {
		t.Errorf("comment retained: %q", out)
	}
}

func TestNormalisesSmartQuotes(t *testing.T) {
	in := "“hello” — world"
	out := compressString(in)
	if strings.ContainsAny(out, "“”—") {
		t.Errorf("smart punctuation retained: %q", out)
	}
}

func TestDedupesConsecutiveLines(t *testing.T) {
	in := "same\nsame\nsame\ndifferent"
	out := compressString(in)
	if strings.Count(out, "same") != 1 {
		t.Errorf("dedupe failed: %q", out)
	}
}

func TestRunRecommendsOnLargeRedundancy(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"model": "gpt-4o-mini",
		"messages": []map[string]any{
			{"role": "user", "content": strings.Repeat("hello world    \n\n\n", 200)},
		},
	})
	c := New(Config{}, tokenizer.NewRegistry())
	recs, err := c.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Body: body,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("recs = %d", len(recs))
	}
	rec := recs[0]
	if rec.EstimatedSavingsTokens < 16 {
		t.Errorf("savings below floor: %d", rec.EstimatedSavingsTokens)
	}
	if rec.QualityScore <= 0 || rec.QualityScore > 0.95 {
		t.Errorf("quality out of range: %f", rec.QualityScore)
	}
	if len(rec.ApplyBody) == 0 {
		t.Error("ApplyBody empty")
	}
	if len(rec.ApplyBody) >= len(body) {
		t.Errorf("ApplyBody not smaller: %d vs %d", len(rec.ApplyBody), len(body))
	}
}

func TestRunSilentBelowThreshold(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o-mini",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	c := New(Config{}, nil)
	recs, _ := c.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Body: body,
	})
	if len(recs) != 0 {
		t.Errorf("tiny content should be silent: %+v", recs)
	}
}

func TestRunUnknownProviderNoOp(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"model": "x", "messages": []map[string]any{{"role": "user", "content": "x"}},
	})
	c := New(Config{}, nil)
	recs, _ := c.Run(context.Background(), &optimizer.Request{
		Provider: "vertex", Body: body,
	})
	if len(recs) != 0 {
		t.Errorf("unknown provider: %+v", recs)
	}
}

func TestRunMalformedBodyNoOp(t *testing.T) {
	c := New(Config{}, nil)
	recs, _ := c.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Body: []byte("not json"),
	})
	if len(recs) != 0 {
		t.Errorf("malformed should be silent: %+v", recs)
	}
}

func TestPreservesArrayContent(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"model": "gpt-4o",
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": strings.Repeat("padded   text   here\n\n\n", 100)},
				{"type": "image_url", "image_url": map[string]any{"url": "..."}},
			},
		}},
	})
	c := New(Config{}, tokenizer.NewRegistry())
	recs, err := c.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Body: body,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected rec for array content")
	}
	// Confirm the rewritten body still has the image part untouched.
	var got map[string]any
	_ = json.Unmarshal(recs[0].ApplyBody, &got)
	msgs := got["messages"].([]any)
	parts := msgs[0].(map[string]any)["content"].([]any)
	hasImage := false
	for _, p := range parts {
		m := p.(map[string]any)
		if m["type"] == "image_url" {
			hasImage = true
		}
	}
	if !hasImage {
		t.Errorf("image part dropped during rewrite")
	}
}

func TestQualityScoreBounds(t *testing.T) {
	if got := qualityScore(0, 0); got != 1.0 {
		t.Errorf("zero before = %f, want 1.0", got)
	}
	if got := qualityScore(1000, 100); got >= 0.95 {
		t.Errorf("aggressive trim should score below 0.95: %f", got)
	}
	if got := qualityScore(1000, 990); got != 0.95 {
		t.Errorf("light trim should clamp at 0.95: %f", got)
	}
}
