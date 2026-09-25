package mcp

import "strings"

// resolveIncludeSources folds allowed `include_sources` into a de-duplicated
// list.
func resolveIncludeSources(sources []string) []string {
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
	if len(out) == 0 {
		return nil
	}
	return out
}
