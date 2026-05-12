package providers

import (
	"encoding/json"
	"strings"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

var anthropicProvider = Provider{
	ID:             eventschema.ProviderAnthropic,
	Prefix:         "/anthropic/",
	DefaultBaseURL: "https://api.anthropic.com",
	Normalize:      normalizeAnthropic,
}

type anthropicMessage struct {
	Role string `json:"role"`
}

type anthropicMessagesRequest struct {
	Model     string             `json:"model"`
	Stream    bool               `json:"stream"`
	MaxTokens int64              `json:"max_tokens"`
	System    any                `json:"system"`
	Messages  []anthropicMessage `json:"messages"`
}

func normalizeAnthropic(path string, body []byte) (CanonicalRequest, error) {
	if !strings.Contains(path, "/v1/messages") {
		return CanonicalRequest{}, ErrUnknownPath
	}
	var req anthropicMessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return CanonicalRequest{}, err
	}
	return CanonicalRequest{
		Provider:        eventschema.ProviderAnthropic,
		Operation:       "messages",
		Model:           req.Model,
		Stream:          req.Stream,
		MaxOutputTokens: req.MaxTokens,
		MessageCount:    len(req.Messages),
		SystemPresent:   req.System != nil,
	}, nil
}
