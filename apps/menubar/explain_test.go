package main

import (
	"encoding/json"
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

// Every provider providers.js (generated from TokenOps' provider registry)
// gives a logo ships one, every shipped logo belongs to such a provider, and
// every logo is a plain SVG: no script, no external reference.
func TestLogosShip(t *testing.T) {
	js, err := fs.ReadFile(frontendFS, "frontend/providers.js")
	if err != nil {
		t.Fatal(err)
	}
	body := string(js)
	start, end := strings.Index(body, "{"), strings.LastIndex(body, "}")
	var providers map[string]struct {
		Name string `json:"name"`
		Logo bool   `json:"logo"`
	}
	if start < 0 || end < start || json.Unmarshal([]byte(body[start:end+1]), &providers) != nil {
		t.Fatal("providers.js is not window.TOKENOPS_PROVIDERS = {JSON};")
	}
	for p, v := range providers {
		if providerNames[p] != v.Name {
			t.Errorf("%s: providers.js says %q, providers_gen.go %q", p, v.Name, providerNames[p])
		}
		if !v.Logo {
			continue
		}
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
	shipped, err := fs.Glob(frontendFS, "frontend/logos/*.svg")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range shipped {
		p := strings.TrimSuffix(strings.TrimPrefix(f, "frontend/logos/"), ".svg")
		if !providers[p].Logo {
			t.Errorf("%s ships but no provider shows it", f)
		}
	}
	if len(providerNames) != len(providers) {
		t.Errorf("providers.js has %d providers, providers_gen.go %d", len(providers), len(providerNames))
	}
}
