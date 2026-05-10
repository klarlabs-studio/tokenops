// Package dedupe hosts the semantic-deduplication optimizer. Multi-turn
// agents frequently re-send identical or near-identical context: a tool
// invocation result pasted into the next prompt, a retrieved document
// echoed across turns, or a system reminder the agent restates verbatim.
// Dedupe finds those repeats, collapses them to a short pointer, and
// reports the projected token savings.
//
// The implementation is local-first: rather than calling out to an
// embedding model, we approximate semantic similarity with a Jaccard
// score over word-trigram shingles. That tracks closely with cosine
// similarity over sentence embeddings on long-form repeated text (the
// dominant pattern in agent traces) while staying within microsecond
// latencies and zero external dependencies. When the project ships an
// embedding backend (see local-model-integ), this package can swap the
// scorer behind the SimilarityFunc seam without touching the optimizer
// pipeline.
package dedupe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/felixgeelhaar/tokenops/internal/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/tokenizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Config tunes the deduper.
type Config struct {
	// SimilarityThreshold is the minimum Jaccard score over word-trigram
	// shingles above which two messages are considered duplicates.
	// Range (0,1]; 1.0 requires exact-text matches. Default 0.85.
	SimilarityThreshold float64
	// MinMessageBytes is the smallest message length (in bytes of
	// extracted text) that is eligible for deduplication. Below this
	// threshold the savings rarely justify the rewrite. Default 200.
	MinMessageBytes int
	// MinSavingsTokens suppresses noisy 1-token recommendations.
	// Default 32.
	MinSavingsTokens int64
	// DropSystemRoles, when false (default), excludes role="system"
	// messages from dedupe — a system prompt re-stated across turns is
	// load-bearing for many agents. Set to true to dedupe system messages
	// too (advanced; couple with a quality gate).
	DropSystemRoles bool
}

// Deduper implements optimizer.Optimizer.
type Deduper struct {
	cfg       Config
	tokenizer *tokenizer.Registry
}

// New constructs a Deduper. tokenizer may be nil — savings then come from
// byte-length deltas (input tokens ≈ bytes/4 fallback).
func New(cfg Config, tk *tokenizer.Registry) *Deduper {
	if cfg.SimilarityThreshold <= 0 || cfg.SimilarityThreshold > 1 {
		cfg.SimilarityThreshold = 0.85
	}
	if cfg.MinMessageBytes <= 0 {
		cfg.MinMessageBytes = 200
	}
	if cfg.MinSavingsTokens <= 0 {
		cfg.MinSavingsTokens = 32
	}
	return &Deduper{cfg: cfg, tokenizer: tk}
}

// Kind reports the optimizer category.
func (d *Deduper) Kind() eventschema.OptimizationType {
	return eventschema.OptimizationTypeDedupe
}

// Run scans req's messages for near-duplicates by Jaccard similarity over
// word-trigram shingles. When at least one duplicate cluster is found,
// emits a recommendation that rewrites the body so each cluster keeps
// only the first occurrence; later occurrences become a short pointer
// like "(duplicate of message #2 — omitted)".
//
// Currently supports OpenAI and Anthropic chat shapes; Gemini's contents
// array can be added later by registering a body-walker.
func (d *Deduper) Run(_ context.Context, req *optimizer.Request) ([]optimizer.Recommendation, error) {
	if req == nil || len(req.Body) == 0 {
		return nil, nil
	}
	switch req.Provider {
	case eventschema.ProviderOpenAI, eventschema.ProviderAnthropic:
	default:
		return nil, nil
	}

	rebuilt, droppedBytes, clusters, ok := dedupeBody(req.Body, d.cfg)
	if !ok || droppedBytes == 0 {
		return nil, nil
	}

	tokensSaved := d.estimateTokensSaved(req.Provider, droppedBytes)
	if tokensSaved < d.cfg.MinSavingsTokens {
		return nil, nil
	}

	return []optimizer.Recommendation{{
		Kind:                   eventschema.OptimizationTypeDedupe,
		EstimatedSavingsTokens: tokensSaved,
		QualityScore:           qualityScore(clusters),
		Reason: fmt.Sprintf(
			"merged %d duplicate message cluster(s); dropped ~%d bytes",
			clusters, droppedBytes),
		ApplyBody: rebuilt,
	}}, nil
}

func (d *Deduper) estimateTokensSaved(provider eventschema.Provider, byteDelta int) int64 {
	if d.tokenizer != nil && byteDelta > 0 {
		canary := strings.Repeat("a ", byteDelta/2)
		if n, err := d.tokenizer.CountText(provider, canary); err == nil {
			return int64(n)
		}
	}
	return int64(byteDelta / 4)
}

// qualityScore is a coarse confidence that the rewrite preserves meaning.
// Single-cluster collapses are very safe (0.95); larger collapses still
// score above the default 0.7 quality gate but flag the higher-impact
// case for review.
func qualityScore(clusters int) float64 {
	switch {
	case clusters <= 1:
		return 0.95
	case clusters <= 3:
		return 0.9
	default:
		return 0.85
	}
}

// --- Jaccard similarity over word-trigram shingles -----------------------

// shingles returns the set of word-trigrams in s. Words are extracted
// with unicode-aware splitting and lower-cased so trivial casing
// differences do not defeat the match.
func shingles(s string) map[string]struct{} {
	words := splitWords(s)
	if len(words) < 3 {
		// Hash unigrams and bigrams as fallback so very short texts still
		// dedupe when literally identical.
		set := make(map[string]struct{}, len(words))
		for _, w := range words {
			set[w] = struct{}{}
		}
		for i := 0; i+1 < len(words); i++ {
			set[words[i]+" "+words[i+1]] = struct{}{}
		}
		return set
	}
	set := make(map[string]struct{}, len(words)-2)
	for i := 0; i+2 < len(words); i++ {
		set[words[i]+" "+words[i+1]+" "+words[i+2]] = struct{}{}
	}
	return set
}

func splitWords(s string) []string {
	out := make([]string, 0, 32)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// jaccard returns |A∩B| / |A∪B|. Returns 0 when both sets are empty.
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	smaller, larger := a, b
	if len(b) < len(a) {
		smaller, larger = b, a
	}
	intersect := 0
	for k := range smaller {
		if _, ok := larger[k]; ok {
			intersect++
		}
	}
	union := len(a) + len(b) - intersect
	if union == 0 {
		return 0
	}
	return float64(intersect) / float64(union)
}

// --- Body manipulation ---------------------------------------------------

type messageProbe struct {
	role    string
	content json.RawMessage
}

// dedupeBody walks an OpenAI/Anthropic chat body's messages, identifies
// duplicate clusters with role-aware Jaccard matching, and rewrites the
// body so every cluster keeps only its first occurrence. Returns:
//
//   - the rebuilt body
//   - number of bytes dropped from message text
//   - number of clusters collapsed (>=1 when a recommendation is emitted)
//   - ok=true when the body was a chat-style request we could parse
func dedupeBody(body []byte, cfg Config) ([]byte, int, int, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, 0, 0, false
	}
	rawMsgs, ok := raw["messages"]
	if !ok {
		return nil, 0, 0, false
	}
	var rawArr []json.RawMessage
	if err := json.Unmarshal(rawMsgs, &rawArr); err != nil {
		return nil, 0, 0, false
	}

	// Extract role + searchable text per message.
	type msgInfo struct {
		role     string
		text     string
		shingles map[string]struct{}
		eligible bool
	}
	infos := make([]msgInfo, len(rawArr))
	for i, m := range rawArr {
		info := msgInfo{}
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(m, &probe); err != nil {
			infos[i] = info
			continue
		}
		if r, ok := probe["role"]; ok {
			_ = json.Unmarshal(r, &info.role)
		}
		if c, ok := probe["content"]; ok {
			info.text = extractText(c)
		}
		info.eligible = len(info.text) >= cfg.MinMessageBytes
		if !cfg.DropSystemRoles && info.role == "system" {
			info.eligible = false
		}
		if info.eligible {
			info.shingles = shingles(info.text)
		}
		infos[i] = info
	}

	// Cluster by similarity. Each message is assigned to the earliest
	// message whose shingle Jaccard score is at or above the threshold;
	// otherwise it forms a new cluster of one.
	cluster := make([]int, len(infos))
	for i := range cluster {
		cluster[i] = i
	}
	for j := 0; j < len(infos); j++ {
		if !infos[j].eligible {
			continue
		}
		for i := 0; i < j; i++ {
			if cluster[i] != i || !infos[i].eligible {
				continue
			}
			if infos[i].role != infos[j].role {
				continue
			}
			if jaccard(infos[i].shingles, infos[j].shingles) >= cfg.SimilarityThreshold {
				cluster[j] = i
				break
			}
		}
	}

	// Rewrite: for every j with cluster[j] != j, replace its content with
	// a short pointer string. Track dropped bytes + clusters.
	totalDropped := 0
	clusterIDs := make(map[int]struct{})
	rebuilt := make([]json.RawMessage, len(rawArr))
	for i, m := range rawArr {
		head := cluster[i]
		if head == i {
			rebuilt[i] = m
			continue
		}
		clusterIDs[head] = struct{}{}
		dropped := len(infos[i].text)
		if dropped <= 0 {
			rebuilt[i] = m
			continue
		}
		// Build a shortened replacement message that preserves role + a
		// pointer placeholder. The placeholder is short enough that the
		// upstream still receives a structurally valid message.
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(m, &probe); err != nil {
			rebuilt[i] = m
			continue
		}
		placeholder := fmt.Sprintf("[duplicate of message #%d — omitted by tokenops dedupe]", head+1)
		newContent, _ := json.Marshal(placeholder)
		probe["content"] = newContent
		encoded, err := json.Marshal(probe)
		if err != nil {
			rebuilt[i] = m
			continue
		}
		rebuilt[i] = encoded
		totalDropped += dropped - len(placeholder)
	}

	if totalDropped <= 0 || len(clusterIDs) == 0 {
		return nil, 0, 0, true
	}

	rebuiltMsgs, err := json.Marshal(rebuilt)
	if err != nil {
		return nil, 0, 0, false
	}
	out := make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		out[k] = v
	}
	out["messages"] = rebuiltMsgs
	final, err := json.Marshal(out)
	if err != nil {
		return nil, 0, 0, false
	}
	return final, totalDropped, len(clusterIDs), true
}

// extractText collapses an OpenAI/Anthropic content field (string or
// array-of-parts) into a single searchable string. Non-text parts (image,
// tool_result envelopes) are skipped — dedupe targets prose.
func extractText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			textRaw, ok := p["text"]
			if !ok {
				continue
			}
			var text string
			if err := json.Unmarshal(textRaw, &text); err != nil {
				continue
			}
			b.WriteString(text)
			b.WriteByte('\n')
		}
		return b.String()
	}
	return ""
}

// Compile-time interface check.
var _ optimizer.Optimizer = (*Deduper)(nil)

// Avoid unused-import flake when optimizer's Recommendation is unreferenced
// after a refactor.
var _ = messageProbe{}
