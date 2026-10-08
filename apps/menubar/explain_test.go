package main

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"
)

// A failed read is told in words: what it means, what is still shown and
// what happens next, never "the daemon is slow" or a raw status line.
func TestExplainSpeaksPlainly(t *testing.T) {
	at := time.Date(2026, 10, 8, 8, 50, 0, 0, time.Local)
	for _, c := range []struct {
		err   error
		shown time.Time
		want  []string
	}{
		{errSlow, at, []string{"taking longer than usual", "as of 08:50", "try again in a minute"}},
		{errSlow, time.Time{}, []string{"taking longer than usual", "try again in a minute"}},
		{errNoDaemon, time.Time{}, []string{"isn't running", "`tokenops daemon install`"}},
		{&statusError{code: 401, msg: "unauthorized"}, at, []string{"didn't accept", "`tokenops daemon restart`"}},
		{&statusError{code: 500, msg: "GET /api/glance: status 500"}, at, []string{"couldn't be fetched", "as of 08:50"}},
		{errors.New("unreadable /Users/x/.tokenops/daemon.url"), time.Time{}, []string{"couldn't be fetched"}},
	} {
		got := explain(c.err, c.shown)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%v: %q lacks %q", c.err, got, w)
			}
		}
		for _, bad := range []string{"daemon is slow", "status 500", "unreadable", "/api/"} {
			if strings.Contains(got, bad) {
				t.Errorf("%v: %q shows %q", c.err, got, bad)
			}
		}
		if c.shown.IsZero() && strings.Contains(got, "as of") {
			t.Errorf("%v: %q claims a reading it does not have", c.err, got)
		}
	}
}

// Every provider the panel names a logo for ships one, and every shipped
// logo is a plain SVG: no script, no external reference.
func TestLogosShip(t *testing.T) {
	for _, p := range []string{"anthropic", "openai", "gemini", "github", "cursor", "openrouter", "deepseek",
		"moonshot", "kimi", "zai", "minimax", "fireworks"} {
		b, err := fs.ReadFile(frontendFS, "frontend/logos/"+p+".svg")
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		s := string(b)
		if !strings.HasPrefix(s, "<svg") || strings.Contains(s, "<script") || strings.Contains(s, "href") {
			t.Errorf("%s: not a plain SVG", p)
		}
	}
	js, err := fs.ReadFile(frontendFS, "frontend/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), `moonshot: 1, kimi: 1, zai: 1, minimax: 1, fireworks: 1`) {
		t.Error("panel.js LOGOS no longer matches the shipped logos")
	}
}
