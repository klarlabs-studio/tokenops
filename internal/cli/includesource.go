package cli

import (
	"fmt"
	"io"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// resolveIncludeSources folds the repeatable --include-source flag into
// the analytics.Filter IncludeSources list.
//
// A name that is not excluded by default is inert rather than fatal —
// the operator asked to see something that is already there, which is
// not an error — but it gets a one-line note on stderr so the flag
// never silently does nothing.
func resolveIncludeSources(warn io.Writer, sources []string) []string {
	seen := make(map[string]bool, len(sources))
	out := make([]string, 0, len(sources))
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

// scratchFlagHelp documents --include-scratch everywhere it appears.
//
// The surfaces carrying it all claim to describe how the operator works,
// and a session run in a throwaway directory had no operator — it was a
// benchmark, a temporary clone, something started in /tmp. Excluding
// those by default is the correction for a measurement that was
// confidently wrong: on one real machine they were 94% of a 7-day window
// and set every grade.
//
// The flag exists because someone eventually wants to measure the
// harness itself, and a default that cannot be turned off is a different
// kind of dishonesty.
const scratchFlagHelp = "include sessions run in throwaway directories (benchmark harnesses, /tmp clones)"
