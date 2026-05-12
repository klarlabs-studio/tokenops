package providers

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

var geminiProvider = Provider{
	ID:             eventschema.ProviderGemini,
	Prefix:         "/gemini/",
	DefaultBaseURL: "https://generativelanguage.googleapis.com",
	Normalize:      normalizeGemini,
}

type geminiContent struct {
	Role string `json:"role"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens int64 `json:"maxOutputTokens"`
}

type geminiGenerateRequest struct {
	Contents          []geminiContent        `json:"contents"`
	SystemInstruction any                    `json:"systemInstruction"`
	GenerationConfig  geminiGenerationConfig `json:"generationConfig"`
}

// modelFromPath extracts the model id from a Gemini-style path:
//
//	/v1/models/gemini-1.5-pro:generateContent
var modelFromPathRE = regexp.MustCompile(`/models/([^:/]+):`)

func normalizeGemini(path string, body []byte) (CanonicalRequest, error) {
	switch {
	case strings.Contains(path, ":generateContent"),
		strings.Contains(path, ":streamGenerateContent"):
		var req geminiGenerateRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return CanonicalRequest{}, err
		}
		c := CanonicalRequest{
			Provider:        eventschema.ProviderGemini,
			Operation:       "generate_content",
			Stream:          strings.Contains(path, ":streamGenerateContent"),
			MaxOutputTokens: req.GenerationConfig.MaxOutputTokens,
			MessageCount:    len(req.Contents),
			SystemPresent:   req.SystemInstruction != nil,
		}
		if m := modelFromPathRE.FindStringSubmatch(path); len(m) == 2 {
			c.Model = m[1]
		}
		return c, nil
	}
	return CanonicalRequest{}, ErrUnknownPath
}
