package prefixlint

import (
	"regexp"
	"sort"
	"strings"
)

// Match is one volatile token in a text.
type Match struct {
	Line  int    `json:"line"`
	Kind  string `json:"kind"`
	Token string `json:"token"`
}

// month names as dates spell them.
const month = `(?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|June?|July?|Aug(?:ust)?|Sep(?:t(?:ember)?)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)`

// valuePatterns are tokens whose value changes: each is the whole token,
// so a match is what is reported. Earlier patterns win where two overlap
// (a timestamp over the date inside it).
var valuePatterns = []struct {
	kind string
	re   *regexp.Regexp
}{
	// `date`'s default output: "Mon Oct 26 10:15:00 CET 2026".
	{KindTimestamp, regexp.MustCompile(`\b(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun) ` + month + ` [ \d]?\d \d{2}:\d{2}:\d{2}(?: [A-Z]{2,5})? \d{4}\b`)},
	// ISO 8601 / RFC 3339 date and time.
	{KindTimestamp, regexp.MustCompile(`\b(?:19|20)\d{2}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])[T ](?:[01]\d|2[0-3]):[0-5]\d(?::[0-5]\d(?:\.\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?`)},
	{KindDate, regexp.MustCompile(`\b(?:19|20)\d{2}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])\b`)},
	{KindDate, regexp.MustCompile(`\b` + month + `\.? \d{1,2}(?:st|nd|rd|th)?,? (?:19|20)\d{2}\b`)},
	{KindDate, regexp.MustCompile(`\b\d{1,2} ` + month + `\.?,? (?:19|20)\d{2}\b`)},
	// A clock time with seconds; "16:9" and "8080:8080" are not.
	{KindTime, regexp.MustCompile(`\b(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d\b`)},
	{KindUUID, regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)},
	{KindCounter, regexp.MustCompile(`(?i)\b(?:session|run|turn|iteration|build|visit|invocation)s? ?(?:count|number|no\.|#) ?:? ?\d+`)},
}

// sessionID is a value labelled as a session id; the value is the token.
var sessionID = regexp.MustCompile(`(?i)\bsession[_ -]?id\s*[:=]\s*([A-Za-z0-9_-]{6,})`)

// gitSHA is a commit hash after a word that names one; a bare hex word
// ("deadbeef", a colour) is not a commit.
var gitSHA = regexp.MustCompile(`(?i)\b(?:commit|head|sha|revision|rev)\b[^\n]{0,24}?\b([0-9a-f]{7,40})\b`)

// markers are phrases that say a line is kept current: what follows them
// changes when it is rewritten.
var markers = []struct {
	kind string
	re   *regexp.Regexp
}{
	{KindToday, regexp.MustCompile(`(?i)\b(?:today is|today's date|current (?:date|time)|the (?:date|time) is)\b`)},
	{KindLastUpdated, regexp.MustCompile(`(?i)\b(?:last (?:updated|modified|synced|refreshed|run)|generated (?:at|on)|updated (?:at|on))\b`)},
}

// ScanText finds the volatile tokens in text, line by line, in order.
func ScanText(text string) []Match {
	lines := strings.Split(text, "\n")
	out := make([]Match, 0, len(lines)/8)
	for i, line := range lines {
		out = append(out, scanLine(line, i+1)...)
	}
	return out
}

type span struct {
	start, end int
	kind       string
}

func scanLine(line string, n int) []Match {
	var spans []span
	taken := func(s, e int) bool {
		for _, x := range spans {
			if s < x.end && e > x.start {
				return true
			}
		}
		return false
	}
	add := func(s, e int, kind string) {
		if !taken(s, e) {
			spans = append(spans, span{s, e, kind})
		}
	}
	for _, p := range valuePatterns {
		for _, loc := range p.re.FindAllStringIndex(line, -1) {
			add(loc[0], loc[1], p.kind)
		}
	}
	for _, loc := range sessionID.FindAllStringSubmatchIndex(line, -1) {
		add(loc[2], loc[3], KindSessionID)
	}
	for _, loc := range gitSHA.FindAllStringSubmatchIndex(line, -1) {
		if h := line[loc[2]:loc[3]]; hasDigitAndLetter(h) {
			add(loc[2], loc[3], KindGitSHA)
		}
	}
	for _, m := range markers {
		for _, loc := range m.re.FindAllStringIndex(line, -1) {
			add(loc[0], loc[1], m.kind)
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	out := make([]Match, 0, len(spans))
	for _, s := range spans {
		out = append(out, Match{Line: n, Kind: s.kind, Token: line[s.start:s.end]})
	}
	return out
}

func hasDigitAndLetter(s string) bool {
	return strings.ContainsAny(s, "0123456789") && strings.ContainsAny(strings.ToLower(s), "abcdef")
}

// isMarker says a kind is a phrase rather than a value.
func isMarker(kind string) bool { return kind == KindToday || kind == KindLastUpdated }
