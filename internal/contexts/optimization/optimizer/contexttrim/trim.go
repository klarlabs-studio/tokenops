// Package contexttrim hosts the context-window trimming optimizer.
// It applies a configurable retention policy — keep the system prompt,
// keep the last N user/assistant turns, drop everything older — to chat
// requests heading upstream. The optimizer rewrites the request body
// (OpenAI / Anthropic / Gemini) so accepted recommendations forward a
// shorter context to the provider; passive runs estimate the savings
// without mutating the payload.
package contexttrim

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/tokenizer"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Config tunes the trimmer. Zero values produce defaults: keep the
// system prompt, keep the last 4 turns (8 messages — user+assistant
// each), require at least 1 turn beyond the policy before recommending.
type Config struct {
	// KeepSystem retains the system prompt regardless of position.
	// Default true.
	KeepSystem *bool
	// KeepLastTurns is the number of trailing user/assistant turns to
	// preserve (a "turn" = one user message + its assistant response).
	// Default 4.
	KeepLastTurns int
	// MinDroppedMessages is the minimum number of messages that must be
	// trimmable before the optimizer emits a recommendation. Avoids
	// noisy "0 saved" events on short conversations. Default 2.
	MinDroppedMessages int
}

// Trimmer is the Optimizer implementation.
type Trimmer struct {
	cfg       Config
	tokenizer *tokenizer.Registry
}

// New constructs a Trimmer. tokenizer may be nil — savings then surface
// as message-count deltas only.
func New(cfg Config, tk *tokenizer.Registry) *Trimmer {
	if cfg.KeepSystem == nil {
		t := true
		cfg.KeepSystem = &t
	}
	if cfg.KeepLastTurns <= 0 {
		cfg.KeepLastTurns = 4
	}
	if cfg.MinDroppedMessages <= 0 {
		cfg.MinDroppedMessages = 2
	}
	return &Trimmer{cfg: cfg, tokenizer: tk}
}

// Kind reports the optimizer category.
func (t *Trimmer) Kind() eventschema.OptimizationType {
	return eventschema.OptimizationTypeContextTrim
}

// Run analyses req and returns at most one trim recommendation.
func (t *Trimmer) Run(_ context.Context, req *optimizer.Request) ([]optimizer.Recommendation, error) {
	if req == nil || len(req.Body) == 0 {
		return nil, nil
	}
	switch req.Provider {
	case eventschema.ProviderOpenAI:
		return t.runOpenAI(req)
	case eventschema.ProviderAnthropic:
		return t.runAnthropic(req)
	default:
		// Gemini and other providers fall through — schemas with
		// systemInstruction at the body root would need their own
		// rewriter; out of scope for the first cut.
		return nil, nil
	}
}

// trimMessages returns kept-by-index for the OpenAI/Anthropic-style
// flat message list. The result is a sorted slice of indices into the
// original messages array.
func (t *Trimmer) trimMessages(roles []string) []int {
	if len(roles) <= t.cfg.KeepLastTurns*2+1 {
		// Not enough material to trim meaningfully.
		return allIndices(len(roles))
	}
	keep := make(map[int]bool, len(roles))
	if *t.cfg.KeepSystem {
		for i, role := range roles {
			if role == "system" {
				keep[i] = true
			}
		}
	}
	// Walk from the tail, counting turns. A turn is a user message; the
	// assistant message that follows ships with it.
	turns := 0
	for i := len(roles) - 1; i >= 0; i-- {
		if turns >= t.cfg.KeepLastTurns {
			break
		}
		keep[i] = true
		if roles[i] == "user" {
			turns++
		}
	}
	out := make([]int, 0, len(keep))
	for i := 0; i < len(roles); i++ {
		if keep[i] {
			out = append(out, i)
		}
	}
	return out
}

func allIndices(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// --- OpenAI ---------------------------------------------------------------

type openAIBody struct {
	// Top-level fields are preserved by round-tripping through a
	// json.RawMessage map; only the messages array is rewritten.
	raw      map[string]json.RawMessage
	Messages []openAIMessage
}

type openAIMessage struct {
	Role string          `json:"role"`
	rest json.RawMessage // full JSON of the message
}

func (t *Trimmer) runOpenAI(req *optimizer.Request) ([]optimizer.Recommendation, error) {
	body, err := parseOpenAIBody(req.Body)
	if err != nil {
		return nil, nil // unparseable — let other optimizers decide
	}
	roles := make([]string, len(body.Messages))
	for i, m := range body.Messages {
		roles[i] = m.Role
	}
	keepIdx := t.trimMessages(roles)
	if len(keepIdx) == len(body.Messages) {
		return nil, nil
	}
	dropped := len(body.Messages) - len(keepIdx)
	if dropped < t.cfg.MinDroppedMessages {
		return nil, nil
	}
	rebuilt := make([]openAIMessage, 0, len(keepIdx))
	for _, idx := range keepIdx {
		rebuilt = append(rebuilt, body.Messages[idx])
	}
	newBody, err := serializeOpenAIBody(body.raw, rebuilt)
	if err != nil {
		return nil, fmt.Errorf("contexttrim: rebuild openai body: %w", err)
	}

	savings := t.estimateSavings(req, body.Messages, keepIdx)
	return []optimizer.Recommendation{{
		Kind:                   eventschema.OptimizationTypeContextTrim,
		EstimatedSavingsTokens: savings,
		QualityScore:           t.qualityScore(dropped),
		Reason:                 fmt.Sprintf("dropped %d of %d messages", dropped, len(body.Messages)),
		ApplyBody:              newBody,
	}}, nil
}

func parseOpenAIBody(b []byte) (*openAIBody, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	rawMsgs, ok := raw["messages"]
	if !ok {
		return nil, fmt.Errorf("no messages field")
	}
	var rawArr []json.RawMessage
	if err := json.Unmarshal(rawMsgs, &rawArr); err != nil {
		return nil, err
	}
	msgs := make([]openAIMessage, len(rawArr))
	for i, r := range rawArr {
		var probe struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(r, &probe); err != nil {
			return nil, err
		}
		msgs[i] = openAIMessage{Role: probe.Role, rest: r}
	}
	return &openAIBody{raw: raw, Messages: msgs}, nil
}

func serializeOpenAIBody(raw map[string]json.RawMessage, msgs []openAIMessage) ([]byte, error) {
	rawArr := make([]json.RawMessage, len(msgs))
	for i, m := range msgs {
		rawArr[i] = m.rest
	}
	encoded, err := json.Marshal(rawArr)
	if err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		out[k] = v
	}
	out["messages"] = encoded
	return json.Marshal(out)
}

// --- Anthropic ------------------------------------------------------------

type anthropicBody struct {
	raw      map[string]json.RawMessage
	Messages []openAIMessage // same shape — role + opaque rest
}

func (t *Trimmer) runAnthropic(req *optimizer.Request) ([]optimizer.Recommendation, error) {
	body, err := parseAnthropicBody(req.Body)
	if err != nil {
		return nil, nil
	}
	// Anthropic's "system" lives at the body root, not inside Messages.
	// Treat the inner messages as a turn list and trim accordingly.
	roles := make([]string, len(body.Messages))
	for i, m := range body.Messages {
		roles[i] = m.Role
	}
	keepIdx := t.trimMessages(roles)
	if len(keepIdx) == len(body.Messages) {
		return nil, nil
	}
	dropped := len(body.Messages) - len(keepIdx)
	if dropped < t.cfg.MinDroppedMessages {
		return nil, nil
	}
	rebuilt := make([]openAIMessage, 0, len(keepIdx))
	for _, idx := range keepIdx {
		rebuilt = append(rebuilt, body.Messages[idx])
	}
	newBody, err := serializeAnthropicBody(body.raw, rebuilt)
	if err != nil {
		return nil, fmt.Errorf("contexttrim: rebuild anthropic body: %w", err)
	}

	savings := t.estimateSavings(req, body.Messages, keepIdx)
	return []optimizer.Recommendation{{
		Kind:                   eventschema.OptimizationTypeContextTrim,
		EstimatedSavingsTokens: savings,
		QualityScore:           t.qualityScore(dropped),
		Reason:                 fmt.Sprintf("dropped %d of %d messages", dropped, len(body.Messages)),
		ApplyBody:              newBody,
	}}, nil
}

func parseAnthropicBody(b []byte) (*anthropicBody, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	rawMsgs, ok := raw["messages"]
	if !ok {
		return nil, fmt.Errorf("no messages field")
	}
	var rawArr []json.RawMessage
	if err := json.Unmarshal(rawMsgs, &rawArr); err != nil {
		return nil, err
	}
	msgs := make([]openAIMessage, len(rawArr))
	for i, r := range rawArr {
		var probe struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(r, &probe); err != nil {
			return nil, err
		}
		msgs[i] = openAIMessage{Role: probe.Role, rest: r}
	}
	return &anthropicBody{raw: raw, Messages: msgs}, nil
}

func serializeAnthropicBody(raw map[string]json.RawMessage, msgs []openAIMessage) ([]byte, error) {
	rawArr := make([]json.RawMessage, len(msgs))
	for i, m := range msgs {
		rawArr[i] = m.rest
	}
	encoded, err := json.Marshal(rawArr)
	if err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		out[k] = v
	}
	out["messages"] = encoded
	return json.Marshal(out)
}

// --- shared estimation ----------------------------------------------------

// estimateSavings computes the token delta between the dropped messages
// and the kept set. Uses the tokenizer when available; falls back to a
// crude byte-length estimate otherwise.
func (t *Trimmer) estimateSavings(req *optimizer.Request, msgs []openAIMessage, keep []int) int64 {
	keptSet := make(map[int]bool, len(keep))
	for _, i := range keep {
		keptSet[i] = true
	}
	dropped := 0
	for i, m := range msgs {
		if keptSet[i] {
			continue
		}
		dropped += len(m.rest)
	}
	if dropped == 0 {
		return 0
	}
	if t.tokenizer != nil {
		// Approximate: count the dropped raw JSON via the provider's
		// tokenizer. Slightly overestimates because of JSON delimiters,
		// which is fine for a savings estimate (rounds up).
		var blob []byte
		for i, m := range msgs {
			if keptSet[i] {
				continue
			}
			blob = append(blob, m.rest...)
			blob = append(blob, '\n')
		}
		if n, err := t.tokenizer.CountText(req.Provider, string(blob)); err == nil {
			return int64(n)
		}
	}
	// Fallback: ~4 bytes per token.
	return int64(dropped / 4)
}

// qualityScore returns a heuristic quality preservation score: trimming
// older context is low-risk for short trims and slightly riskier for
// large ones because we may drop relevant grounding. Capped at 0.95.
func (t *Trimmer) qualityScore(dropped int) float64 {
	score := 0.95 - 0.02*float64(dropped)
	if score < 0.6 {
		score = 0.6
	}
	return score
}
