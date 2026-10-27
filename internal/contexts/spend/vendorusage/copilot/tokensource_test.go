package copilot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

type stubSource struct {
	name  string
	token string
	err   error
	calls *int
}

func (s stubSource) Name() string { return s.name }
func (s stubSource) Token(context.Context) (string, error) {
	if s.calls != nil {
		*s.calls++
	}
	return s.token, s.err
}

// Sources are tried in order; the first token wins and later sources are
// not consulted (a later one may run a CLI).
func TestFirstTokenTakesTheFirstSourceWithAToken(t *testing.T) {
	var ghCalls int
	tok, err := FirstToken(context.Background(), []TokenSource{
		stubSource{name: "apps.json", err: ErrNoToken},
		stubSource{name: "COPILOT_GITHUB_TOKEN", token: "fixture-env"},
		stubSource{name: "gh", token: "fixture-gh", calls: &ghCalls},
	})
	if err != nil || tok != "fixture-env" {
		t.Fatalf("got %q, %v; want the env token", tok, err)
	}
	if ghCalls != 0 {
		t.Errorf("gh was run %d times after a token was found", ghCalls)
	}
}

// With no token anywhere but a source that failed, the failure is the
// answer (with the source named), not "not signed in".
func TestFirstTokenReportsWhyNoTokenWasRead(t *testing.T) {
	_, err := FirstToken(context.Background(), []TokenSource{
		stubSource{name: "apps.json", err: ErrNoToken},
		stubSource{name: "macOS Keychain (copilot-cli)", err: errors.New("macOS would ask to allow this read")},
	})
	if err == nil || errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v; want the Keychain failure", err)
	}
	if !strings.Contains(err.Error(), "copilot-cli") {
		t.Errorf("err %q does not name the source", err)
	}
	if _, err := FirstToken(context.Background(), []TokenSource{stubSource{name: "x", err: ErrNoToken}}); !errors.Is(err, ErrNoToken) {
		t.Errorf("no token anywhere: err = %v, want ErrNoToken", err)
	}
}

// The configured sources come after the apps.json files: an IDE plugin's
// token keeps working exactly as before.
func TestPollerTriesTheExtraSourcesAfterTheFiles(t *testing.T) {
	var got string
	p := NewPoller(nil, PollerOptions{
		TokenPaths:   []string{filepath.Join(t.TempDir(), "missing.json")},
		TokenSources: []TokenSource{stubSource{name: "gh", token: "fixture-gh"}},
		NewClient:    func(tok string) UserSource { got = tok; return fakeSource{body: sampleResponse} },
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := p.ensureClient(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got != "fixture-gh" {
		t.Errorf("client bound to %q; want the gh token", got)
	}
}

// Run used to return at once when no token was found at boot: the
// supervisor does not restart pollers, so Copilot stayed unread until the
// daemon restarted, and nothing was recorded to say why.
func TestRunKeepsLookingForATokenAndSaysWhy(t *testing.T) {
	rec := freshness.NewRecorder()
	p := NewPoller(nil, PollerOptions{
		Health:       rec,
		TokenPaths:   []string{filepath.Join(t.TempDir(), "missing.json")},
		TokenSources: []TokenSource{stubSource{name: "macOS Keychain (copilot-cli)", err: errors.New("macOS would ask to allow this read")}},
		Interval:     time.Hour,
		NewClient:    func(string) UserSource { return fakeSource{body: sampleResponse} },
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := p.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run = %v; want it to keep running until the context ends", err)
	}
	if got := rec.Poll(); got.LastError == nil || !strings.Contains(got.LastError.Error(), "copilot-cli") {
		t.Errorf("health = %+v; want the Keychain failure recorded", got)
	}
}
