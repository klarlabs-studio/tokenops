package providers

import (
	"encoding/json"
	"strings"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

var cohereProvider = Provider{
	ID:             eventschema.ProviderCohere,
	Prefix:         "/cohere/",
	DefaultBaseURL: "https://api.cohere.com",
	Normalize:      normalizeCohere,
}

// cohereV2Request models Cohere's /v2/chat body. Its messages array is
// OpenAI-shaped ([{role, content}]), but the endpoint path and output-limit
// field differ, so it cannot reuse the OpenAI normalizer.
type cohereV2Request struct {
	Model     string `json:"model"`
	Stream    bool   `json:"stream"`
	MaxTokens int64  `json:"max_tokens"`
	Messages  []struct {
		Role string `json:"role"`
	} `json:"messages"`
}

// cohereV1Request models Cohere's legacy /v1/chat body: a single current
// message plus a chat_history array, with the system prompt carried in
// preamble (or a SYSTEM-role history entry).
type cohereV1Request struct {
	Model       string `json:"model"`
	Stream      bool   `json:"stream"`
	MaxTokens   int64  `json:"max_tokens"`
	Message     string `json:"message"`
	Preamble    string `json:"preamble"`
	ChatHistory []struct {
		Role string `json:"role"`
	} `json:"chat_history"`
}

func normalizeCohere(path string, body []byte) (CanonicalRequest, error) {
	switch {
	case strings.Contains(path, "/v2/chat"):
		var req cohereV2Request
		if err := json.Unmarshal(body, &req); err != nil {
			return CanonicalRequest{}, err
		}
		c := CanonicalRequest{
			Provider:        eventschema.ProviderCohere,
			Operation:       "chat",
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

	case strings.Contains(path, "/v1/chat"):
		var req cohereV1Request
		if err := json.Unmarshal(body, &req); err != nil {
			return CanonicalRequest{}, err
		}
		c := CanonicalRequest{
			Provider:        eventschema.ProviderCohere,
			Operation:       "chat",
			Model:           req.Model,
			Stream:          req.Stream,
			MaxOutputTokens: req.MaxTokens,
			// The current message plus each prior history turn.
			MessageCount: len(req.ChatHistory) + 1,
		}
		if req.Preamble != "" {
			c.SystemPresent = true
		}
		for _, h := range req.ChatHistory {
			if strings.EqualFold(h.Role, "system") {
				c.SystemPresent = true
				break
			}
		}
		return c, nil
	}
	return CanonicalRequest{}, ErrUnknownPath
}
