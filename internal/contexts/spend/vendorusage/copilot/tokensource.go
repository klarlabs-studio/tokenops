package copilot

import (
	"context"
	"errors"
	"fmt"
)

// TokenSource is one place a Copilot OAuth token may be kept: the IDE
// plugins' apps.json, an environment variable, the Copilot CLI's Keychain
// item, the GitHub CLI. Token returns ErrNoToken when this source holds
// none, and another error when it holds one it cannot give (the Keychain
// would ask the operator, a file does not parse). The token itself is
// never part of an error.
type TokenSource interface {
	Name() string
	Token(ctx context.Context) (string, error)
}

// FileSource reads apps.json / hosts.json files in order (LoadToken).
type FileSource struct{ Paths []string }

// Name implements TokenSource.
func (FileSource) Name() string { return "Copilot apps.json" }

// Token implements TokenSource.
func (f FileSource) Token(context.Context) (string, error) { return LoadToken(f.Paths) }

// FirstToken walks sources in order and returns the first token found.
// A source with no token is skipped; one that failed is remembered, so
// when no source has a token the operator is told why the one that might
// have did not, instead of "not signed in".
func FirstToken(ctx context.Context, sources []TokenSource) (string, error) {
	var failures []error
	for _, s := range sources {
		tok, err := s.Token(ctx)
		switch {
		case err == nil && tok != "":
			return tok, nil
		case err == nil, errors.Is(err, ErrNoToken):
			continue
		default:
			failures = append(failures, fmt.Errorf("%s: %w", s.Name(), err))
		}
	}
	if len(failures) > 0 {
		return "", errors.Join(failures...)
	}
	return "", ErrNoToken
}
