package providersetup

import (
	"context"
	"errors"
	"strings"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// sessionReader reads only Cookie headers; keyReader only API keys. Both
// belong to one provider, as a vendor read either way does.
type sessionReader struct{ fakeReader }

func (s sessionReader) Read(ctx context.Context, key string) (usage.Reading, error) {
	if !strings.Contains(key, "=") {
		return usage.Reading{}, usage.ErrSkip
	}
	return s.fakeReader.Read(ctx, key)
}

type keyReader struct{ fakeReader }

func (k keyReader) Read(ctx context.Context, key string) (usage.Reading, error) {
	if strings.Contains(key, "=") {
		return usage.Reading{}, usage.ErrSkip
	}
	return k.fakeReader.Read(ctx, key)
}

// A provider read with an API key or a session verifies whichever was
// entered with the reader that reads it.
func TestVerifyFindsTheReaderForTheCredential(t *testing.T) {
	readers := []usage.Reader{
		keyReader{fakeReader{id: "acme", good: "sk-1", reading: usage.Reading{HasBalance: true, BalanceUSD: 1}}},
		sessionReader{fakeReader{id: "acme", good: "sid=2", reading: usage.Reading{HasBalance: true, BalanceUSD: 2}}},
	}
	if lines, err := VerifyWith(context.Background(), readers, "acme", "sid=2"); err != nil || lines[0] != "balance: $2.00" {
		t.Errorf("session: %v %v", lines, err)
	}
	if lines, err := VerifyWith(context.Background(), readers, "acme", "sk-1"); err != nil || lines[0] != "balance: $1.00" {
		t.Errorf("key: %v %v", lines, err)
	}
	if _, err := VerifyWith(context.Background(), readers[:1], "acme", "sid=2"); err == nil || errors.Is(err, ErrRefused) {
		t.Errorf("a session no reader reads = %v", err)
	}
}

type loginReader struct {
	fakeReader
	user, password string
}

func (l loginReader) Login(_ context.Context, user, password string) (string, error) {
	if user != l.user || password != l.password {
		return "", usage.ErrAuth
	}
	return "token-123", nil
}

// A password sign-in returns the vendor's token; the password goes only to
// the reader's sign-in.
func TestLoginReturnsTheToken(t *testing.T) {
	readers := []usage.Reader{loginReader{fakeReader: fakeReader{id: "acme"}, user: "me@x", password: "pw"}}
	if tok, err := LoginWith(context.Background(), readers, "acme", " me@x ", "pw"); err != nil || tok != "token-123" {
		t.Errorf("login = %q %v", tok, err)
	}
	if _, err := LoginWith(context.Background(), readers, "acme", "me@x", "wrong"); !errors.Is(err, ErrRefused) {
		t.Errorf("wrong password = %v", err)
	}
	if _, err := LoginWith(context.Background(), readers, "acme", "me@x", ""); err == nil {
		t.Error("an empty password signed in")
	}
	if _, err := LoginWith(context.Background(), readers, "other", "me@x", "pw"); err == nil {
		t.Error("a provider without a sign-in signed in")
	}
}
