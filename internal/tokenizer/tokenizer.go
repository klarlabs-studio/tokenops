// Package tokenizer estimates token counts for LLM requests and responses
// behind a provider-agnostic interface. The current implementations use
// character-based heuristics tuned per provider; they are intentionally
// approximate (within ~10–15% of the true count for English prose) and
// exist to give the proxy a pre-flight estimate before the upstream
// returns authoritative usage figures.
//
// The Tokenizer interface is the seam where provider-accurate
// implementations (tiktoken for OpenAI, the published Anthropic
// count_tokens API, SentencePiece for Gemini) plug in via Registry.Set
// without rewriting callers.
package tokenizer

import (
	"errors"
	"strings"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// ErrUnknownProvider is returned by Registry.Lookup when no tokenizer is
// registered for the requested provider.
var ErrUnknownProvider = errors.New("tokenizer: no tokenizer for provider")

// Tokenizer estimates token counts. Implementations must be safe for
// concurrent use; the proxy invokes them on every request.
type Tokenizer interface {
	// Provider identifies which LLM family this tokenizer is tuned for.
	Provider() eventschema.Provider
	// CountText returns the estimated token count for a single string.
	CountText(s string) int
	// CountMessages returns the estimated token count for a structured
	// chat message list. Provider-specific message overhead (role tags,
	// turn separators, system prompt envelope) is included.
	CountMessages(msgs []Message) int
}

// Message is the provider-agnostic representation of a chat message
// extracted from a request body. Content is the concatenated text of all
// text parts; multimodal parts (images, audio) are not counted here —
// callers are expected to reflect those separately via attributes.
type Message struct {
	Role    string
	Content string
}

// Registry maps providers to tokenizers. The zero value is usable; Set
// installs an implementation, Lookup retrieves it. Per-model
// specialisations are not modelled here because the heuristics are
// model-agnostic; future tiktoken-backed implementations can branch on
// the model name internally.
type Registry struct {
	tokenizers map[eventschema.Provider]Tokenizer
}

// NewRegistry returns a Registry seeded with default heuristic
// tokenizers for all known providers. Callers can override individual
// providers with Set.
func NewRegistry() *Registry {
	r := &Registry{tokenizers: map[eventschema.Provider]Tokenizer{}}
	r.Set(NewOpenAITokenizer())
	r.Set(NewAnthropicTokenizer())
	r.Set(NewGeminiTokenizer())
	return r
}

// Set installs t under its provider, replacing any previous tokenizer
// for that provider.
func (r *Registry) Set(t Tokenizer) {
	if r.tokenizers == nil {
		r.tokenizers = map[eventschema.Provider]Tokenizer{}
	}
	r.tokenizers[t.Provider()] = t
}

// Lookup returns the tokenizer for p. ErrUnknownProvider is returned
// when nothing is registered.
func (r *Registry) Lookup(p eventschema.Provider) (Tokenizer, error) {
	if r == nil || r.tokenizers == nil {
		return nil, ErrUnknownProvider
	}
	t, ok := r.tokenizers[p]
	if !ok {
		return nil, ErrUnknownProvider
	}
	return t, nil
}

// CountText is a convenience that resolves the tokenizer for p and
// counts s. Returns 0 + ErrUnknownProvider when p is not registered.
func (r *Registry) CountText(p eventschema.Provider, s string) (int, error) {
	t, err := r.Lookup(p)
	if err != nil {
		return 0, err
	}
	return t.CountText(s), nil
}

// CountMessages is the structured counterpart of CountText.
func (r *Registry) CountMessages(p eventschema.Provider, msgs []Message) (int, error) {
	t, err := r.Lookup(p)
	if err != nil {
		return 0, err
	}
	return t.CountMessages(msgs), nil
}

// JoinMessageContent flattens msgs into a single text blob so simple
// tokenizers can fall through to CountText without reimplementing the
// per-message envelope. Provider-aware tokenizers should call this only
// when their own envelope estimate has already been added.
func JoinMessageContent(msgs []Message) string {
	if len(msgs) == 0 {
		return ""
	}
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteByte('\n')
		}
		if m.Role != "" {
			b.WriteString(m.Role)
			b.WriteString(": ")
		}
		b.WriteString(m.Content)
	}
	return b.String()
}
