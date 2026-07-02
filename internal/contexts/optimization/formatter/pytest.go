package formatter

import (
	"fmt"
	"strings"
)

// Pytest compresses `pytest` output. Failures, errors, the failing
// assertion (`>`/`E` detail lines), the FAILURES/ERRORS section headers,
// the short-summary FAILED/ERROR lines, and the terminal "N failed"
// summary are the signal an agent acts on and are always kept. The
// session banner and header block (platform/rootdir/plugins/collected/
// cachedir), the passing progress dots, and verbose PASSED lines are noise
// dropped at Balanced+. At Aggressive, runs of pure-pass progress lines
// ("test_x.py ...... [ nn%]") collapse to a single count so a large green
// run shrinks to its failures.
//
// Progress lines that carry a failure marker (an F) are never dropped:
// losing them would hide which test file failed. Non-pytest output that
// reaches this formatter is handed to the generic noise scrub.
//
// Only "pytest" is registered; `python -m pytest` keys on "python" and is
// out of scope for this formatter.
type Pytest struct{}

// NewPytest returns the pytest formatter.
func NewPytest() *Pytest { return &Pytest{} }

// Command reports the pytest command token.
func (p *Pytest) Command() string { return "pytest" }

// CriticalLine treats failure and error signal as critical: the short
// summary FAILED/ERROR lines, pytest's `E   ` error-detail lines and the
// `> ` failing-source line, the FAILURES/ERRORS section headers, the
// terminal summary when it reports a failure or error, and any `assert`
// line. Passing dots and PASSED markers are never critical.
func (p *Pytest) CriticalLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	switch {
	case strings.HasPrefix(t, "FAILED "),
		strings.HasPrefix(t, "ERROR "),
		strings.HasPrefix(t, "E   "),
		strings.HasPrefix(t, "> "),
		isPytestSectionHeader(t, "FAILURES"),
		isPytestSectionHeader(t, "ERRORS"),
		strings.Contains(t, " failed"),
		strings.Contains(t, " error"),
		strings.Contains(t, "assert"):
		return true
	}
	return false
}

// isPytestSectionHeader reports a "==== NAME ====" banner for the given
// section name (e.g. FAILURES, ERRORS).
func isPytestSectionHeader(t, name string) bool {
	return strings.HasPrefix(t, "===") && strings.Contains(t, name)
}

// looksLikePytest reports whether b resembles pytest output.
func looksLikePytest(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "test session starts") ||
		strings.Contains(s, " passed") ||
		strings.Contains(s, " failed") ||
		strings.Contains(s, "PASSED") ||
		strings.Contains(s, "====")
}

// isPytestHeader reports the session header block lines that carry no
// failure signal and are safe to drop at Balanced+.
func isPytestHeader(t string) bool {
	switch {
	case strings.HasPrefix(t, "platform "),
		strings.HasPrefix(t, "rootdir:"),
		strings.HasPrefix(t, "plugins:"),
		strings.HasPrefix(t, "cachedir:"),
		strings.HasPrefix(t, "collected ") && strings.Contains(t, " item"):
		return true
	}
	return false
}

// isPytestPassed reports a verbose PASSED result line, dropped at Balanced+.
func isPytestPassed(t string) bool {
	return strings.Contains(t, "PASSED")
}

// isPytestPassProgress reports a "<file>.py <markers> [ nn%]" progress line
// whose markers are pure pass dots. A line carrying an F (or any non-dot
// marker) returns false so failure-bearing progress is never dropped.
func isPytestPassProgress(t string) bool {
	if !strings.HasSuffix(t, "%]") {
		return false
	}
	open := strings.LastIndex(t, "[")
	if open < 0 {
		return false
	}
	py := strings.Index(t, ".py ")
	if py < 0 || py+4 > open {
		return false
	}
	markers := strings.TrimSpace(t[py+4 : open])
	if markers == "" {
		return false
	}
	for _, r := range markers {
		if r != '.' && r != ' ' {
			return false
		}
	}
	return true
}

// Format compresses pytest output; non-pytest output falls back to generic.
func (p *Pytest) Format(raw []byte, level LossLevel) (Result, bool) {
	if len(raw) == 0 {
		return Result{}, false
	}
	scrubbed, _ := scrub(raw)
	if !looksLikePytest(scrubbed) {
		res, _ := generic.Format(raw, level)
		res.Notes = "pytest: non-pytest output, generic scrub"
		return res, true
	}
	if level == LossConservative {
		res := enforceCritical(p, raw, scrubbed, 0, "pytest: conservative scrub")
		return res, true
	}

	lines := strings.Split(string(scrubbed), "\n")
	kept := make([]string, 0, len(lines))
	var passProgress []string // pure-pass progress lines (collapsed at Aggressive)
	dropped := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if p.CriticalLine(t) {
			kept = append(kept, line)
			continue
		}
		if isPytestHeader(t) || isPytestPassed(t) {
			dropped++
			continue
		}
		if isPytestPassProgress(t) {
			if level == LossAggressive {
				// Gather pass progress; collapse after we know the count so
				// Aggressive never grows larger than Balanced.
				passProgress = append(passProgress, line)
				continue
			}
			kept = append(kept, line)
			continue
		}
		kept = append(kept, line)
	}

	if len(passProgress) > 0 {
		listing := strings.Join(passProgress, "\n")
		summary := fmt.Sprintf("  (+%d passing test files)", len(passProgress))
		if len(summary) < len(listing) {
			kept = append(kept, summary)
			dropped += len(passProgress)
		} else {
			kept = append(kept, passProgress...)
		}
	}

	compact := []byte(strings.Join(trimBlanks(kept), "\n"))
	notes := fmt.Sprintf("pytest: %s, %d lines dropped", level, dropped)
	res := enforceCritical(p, raw, compact, dropped, notes)
	return res, true
}
