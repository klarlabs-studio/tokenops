package cli

import (
	"fmt"
	"io"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// resolveIncludeSources folds the repeatable --include-source flag and
// the --include-demo back-compat alias into the analytics.Filter
// IncludeSources list.
//
// A name that is not excluded by default is inert rather than fatal —
// the operator asked to see something that is already there, which is
// not an error — but it gets a one-line note on stderr so the flag
// never silently does nothing.
func resolveIncludeSources(warn io.Writer, sources []string, includeDemo bool) []string {
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
	if warn != nil {
		for _, s := range out {
			if !isDefaultExcluded(s) {
				fmt.Fprintf(warn, "note: --include-source=%s has no effect; %q is not excluded by default (excluded: %s)\n",
					s, s, strings.Join(analytics.DefaultExcludedSources, ", "))
			}
		}
	}
	return out
}

func isDefaultExcluded(source string) bool {
	for _, s := range analytics.DefaultExcludedSources {
		if s == source {
			return true
		}
	}
	return false
}
