package formatter

import (
	"fmt"
	"strings"
)

// Nix compresses the output of `nix build` / `nix-build`. Nix is verbose in a
// structured way: it opens a run by announcing the plan ("these N derivations
// will be built:") followed by an indented listing of every ".drv" it will
// realise, prints a "building '/nix/store/…'" marker as it enters each
// derivation, streams the wrapped build's own stdout prefixed with the
// derivation name ("foo> …"), and reports every substituted path
// ("copying path '/nix/store/…' from 'https://cache.nixos.org'…"). The signal
// an agent acts on — the "error:" lines ("error: builder for '…' failed with
// exit code 1", "error: build of '…' failed"), any line reporting a failure or
// an impossibility, and the "these N derivations will be built:" plan summary —
// is always kept.
//
// The per-derivation ".drv" listing under the plan summary, the
// "building '/nix/store/…'" markers, and the "copying path …" substitution
// lines are dropped at Balanced and above. At Aggressive the wrapped
// build-log echo ("foo> …") is dropped too, except any echo line that itself
// carries an error or failure, so a build that only failed shrinks to its
// diagnostics. Every critical line survives at every level.
//
// Output that does not resemble nix is handed to the generic noise scrub, so
// the formatter is never destructive on commands it does not model.
type Nix struct{}

// NewNix returns the nix formatter.
func NewNix() *Nix { return &Nix{} }

// Command reports the nix command token.
func (n *Nix) Command() string { return "nix" }

// CriticalLine treats failure signal and the build plan as critical: any
// "error:" line, any line reporting that something "failed" or "cannot" be
// done, and the "these N derivations will be built:" plan summary. The
// wrapped build-log echo ("foo> …"), the "building '…'" markers, and the
// "copying path …" substitution lines are never critical unless they
// themselves contain an error.
func (n *Nix) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.HasPrefix(t, "error:"),
		strings.Contains(t, "error:"),
		strings.Contains(t, "failed"),
		strings.Contains(t, "cannot"),
		isNixDerivationsSummary(t):
		return true
	}
	return false
}

// isNixDerivationsSummary reports the plan header nix prints before a build
// ("these N derivations will be built:"), kept as a critical summary.
func isNixDerivationsSummary(t string) bool {
	return strings.Contains(t, "derivations will be built")
}

// looksLikeNix reports whether b resembles `nix build` / `nix-build` output.
func looksLikeNix(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "nix") ||
		strings.Contains(s, "/nix/store/") ||
		strings.Contains(s, "derivations will be built") ||
		strings.Contains(s, "building '") ||
		strings.Contains(s, "copying path")
}

// isNixBuilding reports the "building '/nix/store/…'" markers nix prints as it
// enters each derivation, safe to drop at Balanced+.
func isNixBuilding(t string) bool {
	return strings.HasPrefix(t, "building '")
}

// isNixCopying reports the "copying path '/nix/store/…' from '…'" substitution
// lines nix prints when fetching a path from a cache, safe to drop at
// Balanced+.
func isNixCopying(t string) bool {
	return strings.HasPrefix(t, "copying path")
}

// isNixBuildLog reports the wrapped build's own stdout, which nix echoes
// prefixed with the derivation name and "> " ("foo> compiling…"). It is
// dropped at Aggressive unless it carries an error or failure.
func isNixBuildLog(t string) bool {
	i := strings.Index(t, "> ")
	if i <= 0 {
		return false
	}
	// The prefix before "> " is the derivation name: a single token with no
	// spaces. A prose line that happens to contain "> " is not an echo.
	return !strings.Contains(t[:i], " ")
}

// nixLogHasError reports whether a build-log echo carries an error or failure
// signal, so Aggressive keeps it even while dropping the rest of the echo.
func nixLogHasError(t string) bool {
	l := strings.ToLower(t)
	return strings.Contains(l, "error") || strings.Contains(l, "fail")
}

// Format compresses nix output; non-nix output falls back to generic.
func (n *Nix) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikeNix(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "nix: non-nix output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(n, raw, scrubbed, 0, "nix: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	inDerivations := false
	dropped := 0

	for _, line := range lines {
		t := strings.TrimSpace(line)

		// The plan summary opens a block: keep the summary (critical) and drop
		// the indented ".drv" listing lines that follow it.
		if isNixDerivationsSummary(t) {
			inDerivations = true
			kept = append(kept, line)
			continue
		}
		if inDerivations {
			if line != "" && (line[0] == ' ' || line[0] == '\t') {
				dropped++
				continue
			}
			inDerivations = false
		}

		if n.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		// Balanced+: drop the "building '…'" markers and "copying path …"
		// substitution lines.
		if t == "" || isNixBuilding(t) || isNixCopying(t) {
			dropped++
			continue
		}
		// Aggressive: drop the wrapped build-log echo, keeping any echo line
		// that itself carries an error or failure.
		if level == LossAggressive && isNixBuildLog(t) && !nixLogHasError(t) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("nix: %s, %d lines dropped", level, dropped)
	res := enforceCritical(n, raw, compact, dropped, notes)
	return res, true
}
