package claudeusagemeter

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// An expired claude.ai session, with a browser that holds no newer one,
// used to re-read the browser on every poll: a private copy of its cookie
// store each time, and two warnings every five minutes for days. The
// browser is read again only once its cookie store has changed.
func TestPollerRereadsTheBrowserOnlyWhenItsCookiesChange(t *testing.T) {
	errNoCookie := errors.New("browsercookie: no such cookie in this browser")
	noCookie := func() (Session, error) { return Session{}, errNoCookie }
	tests := []struct {
		name string
		// browser answers each read of the browser, in order; the last
		// answer repeats.
		browser []func() (Session, error)
		// stamps is the cookie store's fingerprint before each scan.
		stamps    []string
		wantReads int
		wantRead  int // "could not re-read" lines
		wantGone  int // "had no newer one" lines
		wantPub   int64
	}{
		{
			name:      "no cookie, store unchanged",
			browser:   []func() (Session, error){noCookie},
			stamps:    []string{"a", "a", "a", "a"},
			wantReads: 1, wantRead: 1, wantGone: 1,
		},
		{
			name:      "only the same expired session, store unchanged",
			browser:   []func() (Session, error){func() (Session, error) { return Session{Key: "sk-dead", Browser: "Chrome"}, nil }},
			stamps:    []string{"a", "a", "a", "a"},
			wantReads: 1, wantGone: 1,
		},
		{
			name:      "store changes, still no cookie",
			browser:   []func() (Session, error){noCookie},
			stamps:    []string{"a", "a", "b", "b"},
			wantReads: 2, wantRead: 2, wantGone: 2,
		},
		{
			name: "store changes with a new session",
			browser: []func() (Session, error){
				noCookie,
				func() (Session, error) { return Session{Key: "sk-new", Browser: "Chrome"}, nil },
			},
			stamps:    []string{"a", "a", "b", "b"},
			wantReads: 2, wantRead: 1, wantGone: 1, wantPub: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &fakeMeter{orgs: sampleOrgs, usage: map[string]string{"org-abc": windowedUsage}, dead: "sk-dead"}
			bus := &captureBus{}
			var logs strings.Builder
			reads, scan := 0, 0
			p := NewPoller(bus, PollerOptions{
				SessionKey: "sk-dead",
				OrgID:      "org-abc",
				NewClient:  server.client,
				Interval:   time.Hour,
				Logger:     slog.New(slog.NewTextHandler(&logs, nil)),
				Cookies: func(context.Context) (Session, error) {
					i := min(reads, len(tt.browser)-1)
					reads++
					return tt.browser[i]()
				},
				CookieStamp: func() string { return tt.stamps[scan] },
			})
			if err := p.ensureClient(); err != nil {
				t.Fatal(err)
			}
			for scan = range tt.stamps {
				p.scan(context.Background())
			}
			if reads != tt.wantReads {
				t.Errorf("read the browser %d times, want %d", reads, tt.wantReads)
			}
			if n := strings.Count(logs.String(), "could not re-read the browser session"); n != tt.wantRead {
				t.Errorf("logged the failed read %d times, want %d:\n%s", n, tt.wantRead, logs.String())
			}
			if n := strings.Count(logs.String(), "had no newer one"); n != tt.wantGone {
				t.Errorf("logged the expiry %d times, want %d:\n%s", n, tt.wantGone, logs.String())
			}
			if got := bus.PublishedCount(); got != tt.wantPub {
				t.Errorf("published %d readings, want %d", got, tt.wantPub)
			}
		})
	}
}

// Without a fingerprint of the cookie store the poller cannot tell a
// change, so it keeps reading on every refusal, as before.
func TestPollerWithoutACookieStampReadsOnEveryRefusal(t *testing.T) {
	server := &fakeMeter{orgs: sampleOrgs, usage: map[string]string{"org-abc": windowedUsage}, dead: "sk-dead"}
	reads := 0
	p := NewPoller(&captureBus{}, PollerOptions{
		SessionKey: "sk-dead", OrgID: "org-abc", NewClient: server.client, Interval: time.Hour,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Cookies: func(context.Context) (Session, error) {
			reads++
			return Session{}, errors.New("no cookie")
		},
	})
	if err := p.ensureClient(); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		p.scan(context.Background())
	}
	if reads != 3 {
		t.Errorf("read the browser %d times, want 3", reads)
	}
}

// Every refused poll is still recorded, so the stale-reading finding keeps
// saying which setup reconnects the meter while the log stays quiet.
func TestPollerRecordsEveryExpiredPollWhileLoggingOnce(t *testing.T) {
	server := &fakeMeter{orgs: sampleOrgs, usage: map[string]string{"org-abc": windowedUsage}, dead: "sk-dead"}
	p := NewPoller(&captureBus{}, PollerOptions{
		SessionKey: "sk-dead", OrgID: "org-abc", NewClient: server.client, Interval: time.Hour,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Cookies:     func(context.Context) (Session, error) { return Session{}, errors.New("no cookie") },
		CookieStamp: func() string { return "same" },
	})
	if err := p.ensureClient(); err != nil {
		t.Fatal(err)
	}
	p.scan(context.Background())
	first, _ := p.LastError()
	time.Sleep(2 * time.Millisecond)
	p.scan(context.Background())
	second, err := p.LastError()
	if !errors.Is(err, ErrUnauthorized) || !second.After(first) {
		t.Fatalf("second poll not recorded: %v at %v (first %v)", err, second, first)
	}
}
