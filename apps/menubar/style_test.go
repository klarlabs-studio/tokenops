package main

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// The panel takes its colours, type and motion from the design tokens:
// index.html links tokens.css before style.css, and every --tok- property
// style.css reads is one tokens.css defines.
func TestPanelUsesDesignTokens(t *testing.T) {
	read := func(name string) string {
		b, err := fs.ReadFile(frontendFS, "frontend/"+name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	html, tokens, style := read("index.html"), read("tokens.css"), read("style.css")
	ti, si := strings.Index(html, `href="tokens.css"`), strings.Index(html, `href="style.css"`)
	if ti < 0 || si < 0 || ti > si {
		t.Errorf("index.html must link tokens.css before style.css")
	}
	defined := map[string]bool{}
	for _, m := range regexp.MustCompile(`(--tok-[a-z0-9-]+):`).FindAllStringSubmatch(tokens, -1) {
		defined[m[1]] = true
	}
	used := regexp.MustCompile(`var\((--tok-[a-z0-9-]+)\)`).FindAllStringSubmatch(style, -1)
	if len(used) < 10 {
		t.Fatalf("style.css reads only %d tokens", len(used))
	}
	for _, m := range used {
		if !defined[m[1]] {
			t.Errorf("style.css reads %s, which tokens.css does not define", m[1])
		}
	}
}
