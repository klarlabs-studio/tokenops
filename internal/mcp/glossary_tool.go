package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.klarlabs.de/tokenops/internal/capability/glossary"
)

type explainInput struct {
	Term string `json:"term,omitempty" jsonschema:"description=the figure to explain, e.g. wall-clock, rework, api-equivalent; empty lists every term"`
}

type explainResult struct {
	Term  *glossary.Term  `json:"term,omitempty"`
	Terms []glossary.Term `json:"terms,omitempty"`
}

// RegisterExplainTools mounts tokenops_explain, so an agent can tell the
// operator what a figure means in the same words the terminal uses.
func RegisterExplainTools(s *Server) error {
	if s == nil {
		return errors.New("mcp: nil server")
	}
	s.Tool("tokenops_explain").
		Description("Explain a figure TokenOps shows (wall-clock, turns, rework, first-try, api-equivalent, signal quality, …): what it measures, how it is worked out, how to read it, and its grade bands. Call it when the operator asks what a number means. With no term it lists every term.").
		OutputSchema(explainResult{}).
		Handler(func(_ context.Context, in explainInput) (*explainResult, error) {
			if strings.TrimSpace(in.Term) == "" {
				return &explainResult{Terms: glossary.List()}, nil
			}
			t, ok, suggestions := glossary.Lookup(in.Term)
			if !ok {
				msg := fmt.Sprintf("no term %q", in.Term)
				if len(suggestions) > 0 {
					msg += "; did you mean: " + strings.Join(suggestions, ", ")
				}
				return nil, inputError(errors.New(msg))
			}
			return &explainResult{Term: &t}, nil
		})
	return nil
}
