// Package retrievalprune hosts the retrieval-chunk pruning optimizer.
// Production RAG pipelines stuff retrieved documents into the prompt
// (often as numbered or "---"-separated chunks); when the pipeline
// over-retrieves, the prompt swells with mostly-irrelevant material.
//
// Without access to the upstream retriever's relevance scores, the
// optimizer applies a length-aware heuristic: it identifies chunk
// markers in chat content, counts chunks, and recommends pruning to
// the top-N when the count or aggregate length crosses thresholds.
// "Top-N" is interpreted positionally — most retrievers concatenate
// their results in descending relevance order.
package retrievalprune

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/tokenizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Config tunes the pruner.
type Config struct {
	// MaxChunks is the threshold above which pruning is recommended.
	// Default 8.
	MaxChunks int
	// KeepTopN is the number of chunks retained after pruning. Default 4.
	KeepTopN int
	// MinSavingsTokens suppresses noisy 1-token recommendations.
	// Default 64.
	MinSavingsTokens int64
}

// Pruner implements optimizer.Optimizer.
type Pruner struct {
	cfg       Config
	tokenizer *tokenizer.Registry
}

// New constructs a Pruner.
func New(cfg Config, tk *tokenizer.Registry) *Pruner {
	if cfg.MaxChunks <= 0 {
		cfg.MaxChunks = 8
	}
	if cfg.KeepTopN <= 0 {
		cfg.KeepTopN = 4
	}
	if cfg.KeepTopN >= cfg.MaxChunks {
		cfg.KeepTopN = cfg.MaxChunks - 1
	}
	if cfg.MinSavingsTokens <= 0 {
		cfg.MinSavingsTokens = 64
	}
	return &Pruner{cfg: cfg, tokenizer: tk}
}

// Kind reports the optimizer category.
func (p *Pruner) Kind() eventschema.OptimizationType {
	return eventschema.OptimizationTypeRetrievalPrune
}

// Run inspects req. When a chat message contains chunked retrieval
// material that exceeds MaxChunks, returns a recommendation that
// rewrites the body to keep only the first KeepTopN chunks.
func (p *Pruner) Run(_ context.Context, req *optimizer.Request) ([]optimizer.Recommendation, error) {
	if req == nil || len(req.Body) == 0 {
		return nil, nil
	}
	switch req.Provider {
	case eventschema.ProviderOpenAI, eventschema.ProviderAnthropic:
	default:
		return nil, nil
	}

	rebuilt, droppedBytes, ok := pruneBody(req.Body, p.cfg.MaxChunks, p.cfg.KeepTopN)
	if !ok || droppedBytes == 0 {
		return nil, nil
	}
	tokensSaved := p.estimateTokensSaved(req.Provider, droppedBytes)
	if tokensSaved < p.cfg.MinSavingsTokens {
		return nil, nil
	}
	return []optimizer.Recommendation{{
		Kind:                   eventschema.OptimizationTypeRetrievalPrune,
		EstimatedSavingsTokens: tokensSaved,
		QualityScore:           0.85,
		Reason:                 fmt.Sprintf("pruned retrieval chunks (~%d bytes dropped)", droppedBytes),
		ApplyBody:              rebuilt,
	}}, nil
}

func (p *Pruner) estimateTokensSaved(provider eventschema.Provider, byteDelta int) int64 {
	if p.tokenizer != nil {
		canary := strings.Repeat("a ", byteDelta/2)
		if n, err := p.tokenizer.CountText(provider, canary); err == nil {
			return int64(n)
		}
	}
	return int64(byteDelta / 4)
}

// --- chunk detection ------------------------------------------------------

// chunkSeparator matches the most common retrieval delimiters: a line of
// 3+ dashes ("---"), 3+ equals ("==="), or numbered chunks ("1.", "[1]").
var (
	dashSeparator   = regexp.MustCompile(`(?m)^(?:-{3,}|={3,})\s*$`)
	numberedHeading = regexp.MustCompile(`(?m)^(?:\d+\.\s|\[\d+\]\s)`)
)

// splitChunks returns the chunks of s separated by dashSeparator. When
// no dash separators match, falls back to numbered headings (each
// numbered line starts a new chunk).
func splitChunks(s string) []string {
	if dashSeparator.MatchString(s) {
		raw := dashSeparator.Split(s, -1)
		out := make([]string, 0, len(raw))
		for _, c := range raw {
			c = strings.TrimSpace(c)
			if c != "" {
				out = append(out, c)
			}
		}
		return out
	}
	if matches := numberedHeading.FindAllStringIndex(s, -1); len(matches) > 1 {
		out := make([]string, 0, len(matches))
		for i, m := range matches {
			start := m[0]
			end := len(s)
			if i+1 < len(matches) {
				end = matches[i+1][0]
			}
			out = append(out, strings.TrimSpace(s[start:end]))
		}
		return out
	}
	return nil
}

// joinChunks reconstructs a chunked content string from chunks. The
// chosen delimiter mirrors the most common retriever output ("\n---\n").
func joinChunks(chunks []string) string {
	return strings.Join(chunks, "\n---\n")
}

// pruneBody walks an OpenAI/Anthropic chat body's messages, splits each
// content into chunks, and (when the count exceeds maxChunks) keeps
// only the first keepTopN. Returns the rebuilt body and the number of
// bytes dropped from the original messages array.
func pruneBody(body []byte, maxChunks, keepTopN int) ([]byte, int, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, 0, false
	}
	rawMsgs, ok := raw["messages"]
	if !ok {
		return nil, 0, false
	}
	var rawArr []json.RawMessage
	if err := json.Unmarshal(rawMsgs, &rawArr); err != nil {
		return nil, 0, false
	}
	totalDropped := 0
	rebuilt := make([]json.RawMessage, len(rawArr))
	for i, m := range rawArr {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(m, &probe); err != nil {
			rebuilt[i] = m
			continue
		}
		contentRaw, ok := probe["content"]
		if !ok {
			rebuilt[i] = m
			continue
		}
		newContent, dropped := pruneContent(contentRaw, maxChunks, keepTopN)
		totalDropped += dropped
		probe["content"] = newContent
		encoded, err := json.Marshal(probe)
		if err != nil {
			rebuilt[i] = m
			continue
		}
		rebuilt[i] = encoded
	}
	if totalDropped == 0 {
		return nil, 0, false
	}
	rebuiltMsgs, err := json.Marshal(rebuilt)
	if err != nil {
		return nil, 0, false
	}
	out := make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		out[k] = v
	}
	out["messages"] = rebuiltMsgs
	final, err := json.Marshal(out)
	if err != nil {
		return nil, 0, false
	}
	return final, totalDropped, true
}

func pruneContent(raw json.RawMessage, maxChunks, keepTopN int) (json.RawMessage, int) {
	// String content.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		newStr, dropped := pruneString(s, maxChunks, keepTopN)
		if dropped == 0 {
			return raw, 0
		}
		encoded, _ := json.Marshal(newStr)
		return encoded, dropped
	}
	// Array-of-parts content — only the text parts get pruned.
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err == nil {
		var totalDropped int
		for _, p := range parts {
			textRaw, ok := p["text"]
			if !ok {
				continue
			}
			var text string
			if err := json.Unmarshal(textRaw, &text); err != nil {
				continue
			}
			newText, dropped := pruneString(text, maxChunks, keepTopN)
			if dropped == 0 {
				continue
			}
			totalDropped += dropped
			newRaw, _ := json.Marshal(newText)
			p["text"] = newRaw
		}
		encoded, _ := json.Marshal(parts)
		return encoded, totalDropped
	}
	return raw, 0
}

func pruneString(s string, maxChunks, keepTopN int) (string, int) {
	chunks := splitChunks(s)
	if len(chunks) < maxChunks {
		return s, 0
	}
	keep := keepTopN
	if keep <= 0 || keep >= len(chunks) {
		return s, 0
	}
	dropped := 0
	for _, c := range chunks[keep:] {
		dropped += len(c)
	}
	return joinChunks(chunks[:keep]), dropped
}
