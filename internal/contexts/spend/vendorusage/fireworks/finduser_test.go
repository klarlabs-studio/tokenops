package fireworks

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeAPI is the Reader and UserFinder ports: it records what was asked
// and answers from its fields.
type fakeAPI struct {
	stamp      string
	users      map[string]string // email → user
	findErr    error
	finds      int
	readAcct   string
	readUser   string
	readResult Reading
}

func (f *fakeAPI) KeyStamp(context.Context) (string, error) { return f.stamp, nil }

func (f *fakeAPI) FindUser(_ context.Context, account, email string) (string, string, error) {
	f.finds++
	if account == "" {
		account = "acme"
	}
	if f.findErr != nil {
		return account, "", f.findErr
	}
	u, ok := f.users[email]
	if !ok {
		return account, "", ErrNoUserMatch
	}
	return account, u, nil
}

func (f *fakeAPI) Read(_ context.Context, account, user string, _ time.Time) (Reading, error) {
	f.readAcct, f.readUser = account, user
	r := f.readResult
	if user == "" {
		r.Scope = ScopeAccount
	}
	return r, nil
}

func newPollerFor(api *fakeAPI, home string, opts PollerOptions) *Poller {
	opts.Client, opts.Finder, opts.Home = api, api, home
	if opts.Now == nil {
		opts.Now = func() time.Time { return now }
	}
	return NewPoller(nil, opts)
}

// With no file naming the user, the signed-in email finds them: the
// account comes from the key, the user from the account's members.
func TestPollerFindsTheUserByEmail(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".nexus/config.json"), `{"email":"ada@example.com"}`)
	api := &fakeAPI{stamp: "k1", users: map[string]string{"ada@example.com": "u-ada"}}
	newPollerFor(api, home, PollerOptions{}).Scan(context.Background())
	if api.readAcct != "acme" || api.readUser != "u-ada" {
		t.Errorf("read %q %q", api.readAcct, api.readUser)
	}
}

// The configured email wins over the files; --user-id wins over both.
func TestPollerEmailAndUserPrecedence(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".nexus/config.json"), `{"email":"ada@example.com"}`)
	api := &fakeAPI{stamp: "k1", users: map[string]string{"ada@example.com": "u-ada", "bob@example.com": "u-bob"}}
	newPollerFor(api, home, PollerOptions{Email: "bob@example.com"}).Scan(context.Background())
	if api.readUser != "u-bob" {
		t.Errorf("configured email: read %q", api.readUser)
	}
	api = &fakeAPI{stamp: "k1", users: map[string]string{"ada@example.com": "u-ada"}}
	newPollerFor(api, home, PollerOptions{Email: "bob@example.com", UserID: "u-42"}).Scan(context.Background())
	if api.readUser != "u-42" || api.finds != 0 {
		t.Errorf("user id: read %q after %d lookups", api.readUser, api.finds)
	}
}

// The found user is kept: no lookup on every poll, a new one after a
// day, and at once when the key changes.
func TestPollerCachesTheFoundUser(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".nexus/config.json"), `{"email":"ada@example.com"}`)
	api := &fakeAPI{stamp: "k1", users: map[string]string{"ada@example.com": "u-ada"}}
	clock := now
	p := newPollerFor(api, home, PollerOptions{Now: func() time.Time { return clock }})
	ctx := context.Background()
	p.Scan(ctx)
	clock = clock.Add(15 * time.Minute)
	p.Scan(ctx)
	if api.finds != 1 {
		t.Fatalf("looked up %d times within a day", api.finds)
	}
	clock = clock.Add(24 * time.Hour)
	p.Scan(ctx)
	if api.finds != 2 {
		t.Fatalf("not refreshed after a day: %d lookups", api.finds)
	}
	api.stamp = "k2"
	p.Scan(ctx)
	if api.finds != 3 {
		t.Fatalf("not refreshed on a new key: %d lookups", api.finds)
	}
}

// Each way of not finding the user leaves the reading account-wide and
// says why, in one line.
func TestPollerSaysWhyTheReadingIsAccountWide(t *testing.T) {
	cases := []struct {
		name   string
		email  string
		api    *fakeAPI
		reason string
	}{
		{"no email", "", &fakeAPI{stamp: "k1"}, "your user wasn't found"},
		{"no match", "ada@example.com", &fakeAPI{stamp: "k1"}, "no user on the account has your email"},
		{"lookup refused", "ada@example.com", &fakeAPI{stamp: "k1", findErr: &StatusError{Status: 403, Err: ErrAuth}}, "the user lookup failed (status 403)"},
		{"lookup redirected", "ada@example.com", &fakeAPI{stamp: "k1", findErr: &StatusError{Status: 307}}, "the user lookup failed (status 307)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPollerFor(c.api, t.TempDir(), PollerOptions{Email: c.email})
			r, ok := p.read(context.Background())
			if !ok {
				t.Fatal("no reading")
			}
			if r.Scope != ScopeAccount || !strings.HasPrefix(r.Fallback, c.reason) {
				t.Errorf("scope %q fallback %q, want prefix %q", r.Scope, r.Fallback, c.reason)
			}
			if !strings.Contains(r.Fallback, "--user-id") || strings.Contains(r.Fallback, "\n") {
				t.Errorf("fallback is not one line with the fix: %q", r.Fallback)
			}
		})
	}
}

// The reader's own reason (the user has no cap, their read failed) is
// kept; the poller adds one only when the reader gave none.
func TestPollerKeepsTheReadersReason(t *testing.T) {
	api := &fakeAPI{stamp: "k1", users: map[string]string{"ada@example.com": "u-ada"},
		readResult: Reading{Scope: ScopeAccount, Fallback: FallbackNoCap}}
	r, _ := newPollerFor(api, t.TempDir(), PollerOptions{Email: "ada@example.com"}).read(context.Background())
	if r.Fallback != FallbackNoCap {
		t.Errorf("fallback %q", r.Fallback)
	}
}

// The envelope carries the reason and the block, and the logs never
// carry a key, even when the files the scan reads hold one.
func TestEnvelopeCarriesReasonAndBlock(t *testing.T) {
	until := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	env := NewEnvelope(now, Reading{Scope: ScopeUser, AccountID: "acme", UsedUSD: 105.34, LimitUSD: 100, LimitReached: true, BlockedUntil: until})
	if env.Attributes["extra_usage_blocked_until"] != "2026-11-01T00:00:00Z" || env.Attributes["extra_usage_limit_reached"] != "true" {
		t.Errorf("attrs %v", env.Attributes)
	}
	env = NewEnvelope(now, Reading{Scope: ScopeAccount, AccountID: "acme", Fallback: "your user wasn't found"})
	if env.Attributes["scope_reason"] != "your user wasn't found" {
		t.Errorf("attrs %v", env.Attributes)
	}
	if _, ok := env.Attributes["extra_usage_blocked_until"]; ok {
		t.Errorf("no block, but %v", env.Attributes)
	}
}

func TestScanLogsNoKey(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".nexus/key-storage.json"), `{"apiKey":"fw_s3cret","email":"fw_s3cret@example.com"}`)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	api := &fakeAPI{stamp: "k1", findErr: errors.New("boom")}
	p := newPollerFor(api, home, PollerOptions{Logger: logger})
	p.Scan(context.Background())
	if strings.Contains(buf.String(), "fw_") {
		t.Errorf("logged a key: %s", buf.String())
	}
	if api.finds != 0 {
		t.Errorf("looked up a key-shaped email")
	}
}
