package providers

import (
	"encoding/json"
	"strings"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

var openAIProvider = Provider{
	ID:             eventschema.ProviderOpenAI,
	Prefix:         "/openai/",
	DefaultBaseURL: "https://api.openai.com",
	Normalize:      normalizeOpenAICompatible(eventschema.ProviderOpenAI),
}

// openAIMessage models a single message entry in an OpenAI chat-completions
// request without binding to the entire schema (forward-compatibility).
type openAIMessage struct {
	Role string `json:"role"`
}

type openAIChatRequest struct {
	Model     string          `json:"model"`
	Stream    bool            `json:"stream"`
	MaxTokens int64           `json:"max_tokens"`
	MaxOutput int64           `json:"max_completion_tokens"`
	Messages  []openAIMessage `json:"messages"`
}

type openAIResponsesRequest struct {
	Model     string `json:"model"`
	Stream    bool   `json:"stream"`
	MaxOutput int64  `json:"max_output_tokens"`
	Input     any    `json:"input"`
}

// normalizeOpenAICompatible returns a NormalizeFunc for any upstream that
// speaks the OpenAI /chat/completions (and /responses) wire format. The
// returned CanonicalRequest is stamped with id so metering attributes the
// traffic to the actual provider (openai, groq, deepseek, …) rather than a
// single hardcoded value.
func normalizeOpenAICompatible(id eventschema.Provider) NormalizeFunc {
	return func(path string, body []byte) (CanonicalRequest, error) {
		switch {
		case strings.Contains(path, "/chat/completions"):
			var req openAIChatRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return CanonicalRequest{}, err
			}
			c := CanonicalRequest{
				Provider:        id,
				Operation:       "chat.completions",
				Model:           req.Model,
				Stream:          req.Stream,
				MaxOutputTokens: pickFirstNonZero(req.MaxOutput, req.MaxTokens),
				MessageCount:    len(req.Messages),
			}
			for _, m := range req.Messages {
				if m.Role == "system" {
					c.SystemPresent = true
					break
				}
			}
			return c, nil

		case strings.Contains(path, "/responses"):
			var req openAIResponsesRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return CanonicalRequest{}, err
			}
			return CanonicalRequest{
				Provider:        id,
				Operation:       "responses",
				Model:           req.Model,
				Stream:          req.Stream,
				MaxOutputTokens: req.MaxOutput,
			}, nil
		}
		return CanonicalRequest{}, ErrUnknownPath
	}
}

func pickFirstNonZero(values ...int64) int64 {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}
