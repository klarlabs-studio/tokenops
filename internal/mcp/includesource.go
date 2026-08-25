package mcp

import "strings"

// resolveIncludeSources folds the `include_sources` list and the
// `include_demo` back-compat alias into one de-duplicated list, in the
// order the caller named them. Returns nil when nothing was opted in,
// so the analytics defaults apply untouched.
func resolveIncludeSources(sources []string, includeDemo bool) []string {
	seen := make(map[string]bool, len(sources)+1)
	out := make([]string, 0, len(sources)+1)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, s := range sources {
		add(s)
	}
	if includeDemo {
		add("demo")
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// demoOptedIn reports whether the caller asked for synthetic seeds,
// under either spelling. The synthetic-data banner is suppressed only
// then — opting into mcp-session says nothing about demo data.
func demoOptedIn(sources []string, includeDemo bool) bool {
	if includeDemo {
		return true
	}
	for _, s := range sources {
		if strings.TrimSpace(s) == "demo" {
			return true
		}
	}
	return false
}
