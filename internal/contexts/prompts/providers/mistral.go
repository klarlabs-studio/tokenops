package providers

import (
	"encoding/json"
	"strings"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// mistralProvider proxies api.mistral.ai. The chat-completions surface
// is OpenAI-compatible (POST /v1/chat/completions with model/messages/
// max_tokens), so the normalizer mirrors the OpenAI chat shape; the
// FIM endpoint (/v1/fim/completions, codestral) carries model +
// max_tokens without messages.
var mistralProvider = Provider{
	ID:             eventschema.ProviderMistral,
	Prefix:         "/mistral/",
	DefaultBaseURL: "https://api.mistral.ai",
	Normalize:      normalizeMistral,
}

type mistralChatRequest struct {
	Model     string          `json:"model"`
	Stream    bool            `json:"stream"`
	MaxTokens int64           `json:"max_tokens"`
	Messages  []openAIMessage `json:"messages"`
}

type mistralFIMRequest struct {
	Model     string `json:"model"`
	Stream    bool   `json:"stream"`
	MaxTokens int64  `json:"max_tokens"`
}

func normalizeMistral(path string, body []byte) (CanonicalRequest, error) {
	switch {
	case strings.Contains(path, "/chat/completions"):
		var req mistralChatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return CanonicalRequest{}, err
		}
		c := CanonicalRequest{
			Provider:        eventschema.ProviderMistral,
			Operation:       "chat.completions",
			Model:           req.Model,
			Stream:          req.Stream,
			MaxOutputTokens: req.MaxTokens,
			MessageCount:    len(req.Messages),
		}
		for _, m := range req.Messages {
			if m.Role == "system" {
				c.SystemPresent = true
				break
			}
		}
		return c, nil

	case strings.Contains(path, "/fim/completions"):
		var req mistralFIMRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return CanonicalRequest{}, err
		}
		return CanonicalRequest{
			Provider:        eventschema.ProviderMistral,
			Operation:       "fim.completions",
			Model:           req.Model,
			Stream:          req.Stream,
			MaxOutputTokens: req.MaxTokens,
		}, nil
	}
	return CanonicalRequest{}, ErrUnknownPath
}
