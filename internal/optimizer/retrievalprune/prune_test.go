package retrievalprune

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/felixgeelhaar/tokenops/internal/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/tokenizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func mkBodyWithChunks(numChunks, charsPerChunk int) []byte {
	chunks := make([]string, numChunks)
	for i := range chunks {
		chunks[i] = strings.Repeat("retrieval chunk content ", charsPerChunk/24)
	}
	content := strings.Join(chunks, "\n---\n")
	body, _ := json.Marshal(map[string]any{
		"model": "gpt-4o-mini",
		"messages": []map[string]any{
			{"role": "user", "content": content},
		},
	})
	return body
}

func TestSplitChunksDashSeparated(t *testing.T) {
	in := "chunk-A\n---\nchunk-B\n---\nchunk-C"
	chunks := splitChunks(in)
	if len(chunks) != 3 {
		t.Fatalf("got %d, want 3: %+v", len(chunks), chunks)
	}
}

func TestSplitChunksNumberedFallback(t *testing.T) {
	in := "1. first chunk\nbody\n\n2. second chunk\nmore\n\n3. third\nfinal"
	chunks := splitChunks(in)
	if len(chunks) != 3 {
		t.Fatalf("got %d, want 3: %+v", len(chunks), chunks)
	}
}

func TestSplitChunksNoneReturnsNil(t *testing.T) {
	chunks := splitChunks("just plain prose with no markers")
	if chunks != nil {
		t.Errorf("expected nil, got %+v", chunks)
	}
}

func TestRunPrunesOversizedRetrieval(t *testing.T) {
	body := mkBodyWithChunks(10, 200) // 10 chunks > MaxChunks default 8
	p := New(Config{}, tokenizer.NewRegistry())
	recs, err := p.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Body: body,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("recs = %d", len(recs))
	}
	rec := recs[0]
	if rec.EstimatedSavingsTokens < 64 {
		t.Errorf("savings below floor: %d", rec.EstimatedSavingsTokens)
	}
	// Rewritten body should be smaller.
	if len(rec.ApplyBody) >= len(body) {
		t.Errorf("ApplyBody not smaller: %d vs %d", len(rec.ApplyBody), len(body))
	}
	// And contain only KeepTopN chunks (default 4).
	var got map[string]any
	_ = json.Unmarshal(rec.ApplyBody, &got)
	msgs := got["messages"].([]any)
	content := msgs[0].(map[string]any)["content"].(string)
	chunks := splitChunks(content)
	if len(chunks) != 4 {
		t.Errorf("kept %d chunks, want 4: %+v", len(chunks), chunks)
	}
}

func TestRunBelowMaxChunksNoOp(t *testing.T) {
	body := mkBodyWithChunks(3, 200)
	p := New(Config{MaxChunks: 8}, nil)
	recs, _ := p.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Body: body,
	})
	if len(recs) != 0 {
		t.Errorf("under-limit body should not trigger: %+v", recs)
	}
}

func TestRunUnknownProviderNoOp(t *testing.T) {
	body := mkBodyWithChunks(20, 100)
	p := New(Config{}, nil)
	recs, _ := p.Run(context.Background(), &optimizer.Request{
		Provider: "vertex", Body: body,
	})
	if len(recs) != 0 {
		t.Errorf("unknown provider: %+v", recs)
	}
}

func TestRunMalformedBodyNoOp(t *testing.T) {
	p := New(Config{}, nil)
	recs, _ := p.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Body: []byte("not json"),
	})
	if len(recs) != 0 {
		t.Errorf("malformed: %+v", recs)
	}
}

func TestRunBelowSavingsThresholdNoOp(t *testing.T) {
	// Just over MaxChunks but tiny per-chunk content — savings below the
	// 64-token floor.
	body := mkBodyWithChunks(10, 8)
	p := New(Config{}, tokenizer.NewRegistry())
	recs, _ := p.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Body: body,
	})
	if len(recs) != 0 {
		t.Errorf("tiny content should not fire: %+v", recs)
	}
}

func TestKeepTopNCappedBelowMaxChunks(t *testing.T) {
	p := New(Config{MaxChunks: 4, KeepTopN: 10}, nil)
	if p.cfg.KeepTopN >= p.cfg.MaxChunks {
		t.Errorf("KeepTopN should be capped: %d vs %d", p.cfg.KeepTopN, p.cfg.MaxChunks)
	}
}

func TestPreservesTopLevelFields(t *testing.T) {
	chunks := make([]string, 12)
	for i := range chunks {
		chunks[i] = strings.Repeat("c ", 100)
	}
	content := strings.Join(chunks, "\n---\n")
	body, _ := json.Marshal(map[string]any{
		"model":       "gpt-4o-mini",
		"temperature": 0.3,
		"tool_choice": "auto",
		"messages":    []map[string]any{{"role": "user", "content": content}},
	})
	p := New(Config{}, tokenizer.NewRegistry())
	recs, _ := p.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Body: body,
	})
	if len(recs) != 1 {
		t.Fatalf("expected rec")
	}
	var got map[string]any
	_ = json.Unmarshal(recs[0].ApplyBody, &got)
	if got["temperature"] != 0.3 || got["tool_choice"] != "auto" {
		t.Errorf("top-level fields lost: %+v", got)
	}
}
