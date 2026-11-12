package copilot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// signIn is a token source whose token the test changes between polls,
// as an operator signing in again does.
type signIn struct{ token *string }

func (signIn) Name() string { return "Copilot CLI sign-in" }
func (s signIn) Token(context.Context) (string, error) {
	if *s.token == "" {
		return "", ErrNoToken
	}
	return *s.token, nil
}

// github answers a user record for every token but the ones it refuses,
// and remembers which token each request carried.
type github struct {
	token  string
	answer map[string]error
	sent   *[]string
}

func (g github) User(context.Context) (*UserResponse, error) {
	*g.sent = append(*g.sent, g.token)
	if err := g.answer[g.token]; err != nil {
		return nil, err
	}
	return fakeSource{body: sampleResponse}.User(context.Background())
}

// A token GitHub refused is not sent again: the poller looks for the
// token again, binds a client to a new one as soon as it is found, and
// until then sends nothing and says why. A token that keeps working, or
// a failure that says nothing about the token, keeps the client it has.
func TestPollerDropsARefusedToken(t *testing.T) {
	refused := fmt.Errorf("copilot user: %w: status 401", ErrUnauthorized)
	for _, tc := range []struct {
		name string
		// found is the token the sources hold at each poll.
		found  []string
		answer map[string]error
		// sent is the token each request carried, in order; built the
		// tokens a client was bound to.
		sent, built []string
		// lastErr is a part of the error the last poll recorded; empty
		// is a poll that succeeded.
		lastErr string
	}{
		{
			name:  "the token keeps working",
			found: []string{"tok-a", "tok-a", "tok-a"},
			sent:  []string{"tok-a", "tok-a", "tok-a"}, built: []string{"tok-a"},
		},
		{
			name:  "a failure that is not a refusal keeps the client",
			found: []string{"tok-a", "tok-a", "tok-a"}, answer: map[string]error{"tok-a": errors.New("copilot user: status 502")},
			sent: []string{"tok-a", "tok-a", "tok-a"}, built: []string{"tok-a"}, lastErr: "502",
		},
		{
			name:  "revoked, then signed in again",
			found: []string{"tok-a", "tok-a", "tok-b", "tok-b"}, answer: map[string]error{"tok-a": refused},
			sent: []string{"tok-a", "tok-b", "tok-b"}, built: []string{"tok-a", "tok-b"},
		},
		{
			name:  "revoked, and nothing new signed in",
			found: []string{"tok-a", "tok-a", "tok-a"}, answer: map[string]error{"tok-a": refused},
			sent: []string{"tok-a"}, built: []string{"tok-a"}, lastErr: "refused",
		},
		{
			name:  "revoked, then signed out",
			found: []string{"tok-a", "", ""}, answer: map[string]error{"tok-a": refused},
			sent: []string{"tok-a"}, built: []string{"tok-a"}, lastErr: ErrNoToken.Error(),
		},
		{
			name:  "the new token is refused too",
			found: []string{"tok-a", "tok-b", "tok-b"}, answer: map[string]error{"tok-a": refused, "tok-b": refused},
			sent: []string{"tok-a", "tok-b"}, built: []string{"tok-a", "tok-b"}, lastErr: "refused",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var current string
			var sent, built []string
			p := NewPoller(nil, PollerOptions{
				TokenPaths:   []string{filepath.Join(t.TempDir(), "missing.json")},
				TokenSources: []TokenSource{signIn{token: &current}},
				NewClient: func(tok string) UserSource {
					built = append(built, tok)
					return github{token: tok, answer: tc.answer, sent: &sent}
				},
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			for _, tok := range tc.found {
				current = tok
				p.scan(context.Background())
			}
			if !slices.Equal(sent, tc.sent) {
				t.Errorf("requests carried %v, want %v", sent, tc.sent)
			}
			if !slices.Equal(built, tc.built) {
				t.Errorf("clients bound to %v, want %v", built, tc.built)
			}
			_, err := p.LastError()
			switch {
			case tc.lastErr == "" && err != nil:
				t.Errorf("last poll failed with %v, want it to succeed", err)
			case tc.lastErr != "" && (err == nil || !strings.Contains(err.Error(), tc.lastErr)):
				t.Errorf("last poll recorded %v, want it to say %q", err, tc.lastErr)
			}
		})
	}
}

// A configured token GitHub refused is not sent again either: the
// configuration is what has to change.
func TestPollerDoesNotResendARefusedConfiguredToken(t *testing.T) {
	var sent []string
	p := NewPoller(nil, PollerOptions{
		OAuthToken: "tok-config",
		NewClient: func(tok string) UserSource {
			return github{token: tok, answer: map[string]error{tok: ErrUnauthorized}, sent: &sent}
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	for range 3 {
		p.scan(context.Background())
	}
	if len(sent) != 1 {
		t.Errorf("the refused token was sent %d times, want once", len(sent))
	}
	if _, err := p.LastError(); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("last poll recorded %v, want the refusal", err)
	}
}
