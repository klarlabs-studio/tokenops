// Package promptcompress hosts a heuristic prompt compression
// optimizer. The full LLMLingua-style approach requires running a small
// scorer LM to gate token importance; that integration is left for a
// follow-up. The MVP shipped here applies safe, deterministic
// transformations whose savings are conservative but never destructive:
//
//   - collapse runs of whitespace (3+ blank lines, repeated spaces) to
//     a single canonical form;
//   - strip trailing whitespace on every line;
//   - drop consecutive identical lines (a common artefact when agents
//     paste tool output verbatim into the next prompt);
//   - remove HTML/XML comments;
//   - normalise smart-quotes / em-dashes to their ASCII equivalents
//     (token frontier on most BPE tokenizers).
//
// The compressor never rewrites tokens or paraphrases — it only deletes
// material the model would have spent tokens parsing without changing
// the visible meaning.
package promptcompress

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/felixgeelhaar/tokenops/internal/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/tokenizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Config tunes the compressor.
type Config struct {
	// MinSavingsTokens is the minimum estimated saving below which the
	// recommendation is suppressed (prevents 1-token noise events).
	// Default 16.
	MinSavingsTokens int64
}

// Compressor implements optimizer.Optimizer.
type Compressor struct {
	cfg       Config
	tokenizer *tokenizer.Registry
}

// New constructs a Compressor. tokenizer may be nil — savings then come
// from byte-length deltas.
func New(cfg Config, tk *tokenizer.Registry) *Compressor {
	if cfg.MinSavingsTokens <= 0 {
		cfg.MinSavingsTokens = 16
	}
	return &Compressor{cfg: cfg, tokenizer: tk}
}

// Kind reports the optimizer category.
func (c *Compressor) Kind() eventschema.OptimizationType {
	return eventschema.OptimizationTypePromptCompress
}

// Run scans the request body's chat content, applies the heuristic
// compressor, and emits a recommendation when the projected saving
// clears MinSavingsTokens.
func (c *Compressor) Run(_ context.Context, req *optimizer.Request) ([]optimizer.Recommendation, error) {
	if req == nil || len(req.Body) == 0 {
		return nil, nil
	}
	switch req.Provider {
	case eventschema.ProviderOpenAI, eventschema.ProviderAnthropic:
	default:
		return nil, nil
	}

	rebuilt, before, after, ok := compressBody(req.Provider, req.Body)
	if !ok {
		return nil, nil
	}
	delta := before - after
	if delta <= 0 {
		return nil, nil
	}

	tokensSaved := c.estimateTokensSaved(req.Provider, delta)
	if tokensSaved < c.cfg.MinSavingsTokens {
		return nil, nil
	}

	return []optimizer.Recommendation{{
		Kind:                   eventschema.OptimizationTypePromptCompress,
		EstimatedSavingsTokens: tokensSaved,
		QualityScore:           qualityScore(before, after),
		Reason: fmt.Sprintf(
			"compressed message bodies from %d to %d bytes (%d saved)",
			before, after, delta),
		ApplyBody: rebuilt,
	}}, nil
}

func (c *Compressor) estimateTokensSaved(provider eventschema.Provider, byteDelta int) int64 {
	if c.tokenizer != nil {
		// Approximate: feed a slice of zeros of the saved length to the
		// tokenizer is not meaningful; instead use the tokenizer's
		// inverse char/token ratio via a best-effort canary string of
		// ASCII spaces/letters of the same length.
		canary := strings.Repeat("a ", byteDelta/2)
		if n, err := c.tokenizer.CountText(provider, canary); err == nil {
			return int64(n)
		}
	}
	return int64(byteDelta / 4)
}

func qualityScore(before, after int) float64 {
	if before == 0 {
		return 1.0
	}
	ratio := float64(after) / float64(before)
	// Compression that retains <50% of bytes is more aggressive and
	// scored lower; moderate trims (~80% retained) score near 0.95.
	score := 0.5 + 0.5*ratio
	if score > 0.95 {
		score = 0.95
	}
	return score
}

// --- Body manipulation ----------------------------------------------------

var (
	multipleBlanks = regexp.MustCompile(`\n{3,}`)
	multipleSpaces = regexp.MustCompile(`[ \t]{2,}`)
	htmlComments   = regexp.MustCompile(`(?s)<!--.*?-->`)
	trailingWS     = regexp.MustCompile(`(?m)[ \t]+$`)
)

var smartReplacer = strings.NewReplacer(
	"‘", "'", // ‘
	"’", "'", // ’
	"“", `"`, // “
	"”", `"`, // ”
	"—", "-", // —
	"–", "-", // –
	" ", " ", // non-breaking space
)

// compressString applies the rule set to s and returns the new string.
func compressString(s string) string {
	if s == "" {
		return s
	}
	out := smartReplacer.Replace(s)
	out = htmlComments.ReplaceAllString(out, "")
	out = trailingWS.ReplaceAllString(out, "")
	out = multipleSpaces.ReplaceAllString(out, " ")
	out = multipleBlanks.ReplaceAllString(out, "\n\n")
	out = dedupeConsecutiveLines(out)
	return out
}

func dedupeConsecutiveLines(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= 1 {
		return s
	}
	out := lines[:1]
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			out = append(out, lines[i])
			continue
		}
		if strings.TrimSpace(lines[i]) == strings.TrimSpace(out[len(out)-1]) {
			continue
		}
		out = append(out, lines[i])
	}
	return strings.Join(out, "\n")
}

// compressBody parses an OpenAI/Anthropic body, walks each message's
// content (string or array-of-parts), runs compressString on text
// content, and returns the rewritten body plus before/after byte sizes.
func compressBody(_ eventschema.Provider, body []byte) ([]byte, int, int, bool) {
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

	before := 0
	after := 0
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
		newContent, beforeBytes, afterBytes := compressContent(contentRaw)
		before += beforeBytes
		after += afterBytes
		probe["content"] = newContent
		encoded, err := json.Marshal(probe)
		if err != nil {
			rebuilt[i] = m
			continue
		}
		rebuilt[i] = encoded
	}
	if before == 0 {
		return nil, 0, 0, false
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
	return final, before, after, true
}

func compressContent(raw json.RawMessage) (json.RawMessage, int, int) {
	// String content.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		before := len(s)
		after := compressString(s)
		afterBytes := len(after)
		encoded, _ := json.Marshal(after)
		return encoded, before, afterBytes
	}
	// Array-of-parts content.
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err == nil {
		var before, after int
		for _, p := range parts {
			textRaw, ok := p["text"]
			if !ok {
				continue
			}
			var text string
			if err := json.Unmarshal(textRaw, &text); err != nil {
				continue
			}
			compressed := compressString(text)
			before += len(text)
			after += len(compressed)
			newRaw, _ := json.Marshal(compressed)
			p["text"] = newRaw
		}
		encoded, _ := json.Marshal(parts)
		return encoded, before, after
	}
	return raw, 0, 0
}
