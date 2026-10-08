package accounts

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

// keyOnlyReader reads API keys only and declines anything else.
type keyOnlyReader struct{ fakeReader }

func (*keyOnlyReader) KeyOnly() bool { return true }
func (k *keyOnlyReader) Read(ctx context.Context, key string) (Reading, error) {
	if !strings.HasPrefix(key, "sk-") {
		return Reading{}, ErrSkip
	}
	return k.fakeReader.Read(ctx, key)
}

// A reader of the vendor's API key is never handed a browser session: a
// stored session is declined without counting as a failure, and the
// browser is not re-read for it.
func TestPollerSkipsCredentialsAReaderDoesNotRead(t *testing.T) {
	resolved := 0
	r := &keyOnlyReader{fakeReader{endpoint: "acme", good: "sk-key", reading: Reading{Scope: "account", BalanceUSD: 1, HasBalance: true}}}
	rec := freshness.NewRecorder()
	p := NewPoller(&captureBus{}, PollerOptions{
		Readers: []Reader{r},
		Health:  func(string) *freshness.Recorder { return rec },
		Credentials: func() []Credential {
			return []Credential{
				{Endpoint: "acme", Key: "session=abc"},
				{Endpoint: "acme", Resolve: func(context.Context) (string, error) { resolved++; return "session=new", nil }},
			}
		},
	})
	p.Scan(context.Background())
	if resolved != 0 || len(r.keys) != 0 {
		t.Errorf("resolved %d, keys %v", resolved, r.keys)
	}
	if poll := rec.Poll(); poll.LastError != nil || !poll.LastAttemptAt.IsZero() {
		t.Errorf("a declined credential counted as an attempt: %+v", poll)
	}
}

// A refused session says what to do about it in the source's health, and
// never carries the session itself.
func TestPollerAddsTheRemedyToARefusal(t *testing.T) {
	r := &fakeReader{endpoint: "acme", good: "never"}
	rec := freshness.NewRecorder()
	p := NewPoller(&captureBus{}, PollerOptions{
		Readers: []Reader{r},
		Health:  func(string) *freshness.Recorder { return rec },
		Credentials: func() []Credential {
			return []Credential{{Endpoint: "acme", Key: "session=old", Remedy: "run `tokenops vendor-usage setup acme` again"}}
		},
	})
	p.Scan(context.Background())
	err := rec.Poll().LastError
	if !errors.Is(err, ErrAuth) || !strings.Contains(err.Error(), "setup acme` again") || strings.Contains(err.Error(), "session=old") {
		t.Errorf("health error %v", err)
	}
}
