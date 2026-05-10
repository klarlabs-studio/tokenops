package redaction

import (
	"sort"
	"strings"
)

// Config tunes the Redactor. Zero values produce sensible defaults: the
// built-in rule set runs, entropy detection is enabled at the standard
// thresholds, and emails are redacted.
type Config struct {
	// Rules overrides DefaultRules when non-nil. Use nil to inherit
	// defaults; an empty (non-nil) slice disables pattern detection.
	Rules []Rule
	// EntropyEnabled toggles the entropy fallback detector. Default true.
	EntropyEnabled *bool
	// MinEntropyTokenLen is the minimum length a whitespace-delimited
	// token must have to be considered for entropy scoring. Default 20.
	MinEntropyTokenLen int
	// EntropyThreshold is the minimum bits/char a token must reach before
	// it is flagged as high entropy. Default 4.5 — tuned to flag random
	// 20+ char strings while leaving English prose untouched.
	EntropyThreshold float64
}

// Finding describes a single redaction. Offsets are relative to the input
// string passed to Redact.
type Finding struct {
	Kind  Kind
	Start int
	End   int
	Match string
}

// Redactor replaces secrets in strings with typed placeholders. It is safe
// for concurrent use after construction.
type Redactor struct {
	rules            []Rule
	entropyEnabled   bool
	minTokenLen      int
	entropyThreshold float64
}

// New builds a Redactor from cfg. The returned value is immutable; mutating
// cfg afterwards has no effect.
func New(cfg Config) *Redactor {
	rules := cfg.Rules
	if rules == nil {
		rules = DefaultRules()
	}
	enabled := true
	if cfg.EntropyEnabled != nil {
		enabled = *cfg.EntropyEnabled
	}
	minLen := cfg.MinEntropyTokenLen
	if minLen <= 0 {
		minLen = 20
	}
	threshold := cfg.EntropyThreshold
	if threshold <= 0 {
		threshold = 4.5
	}
	return &Redactor{
		rules:            rules,
		entropyEnabled:   enabled,
		minTokenLen:      minLen,
		entropyThreshold: threshold,
	}
}

// Default returns a Redactor configured with the built-in rules and entropy
// fallback enabled.
func Default() *Redactor { return New(Config{}) }

// Redact returns s with every detected secret replaced by its placeholder
// and the list of findings (in input order). Non-overlapping matches are
// preserved; overlapping matches are resolved by taking the earliest start
// and the longest match.
func (r *Redactor) Redact(s string) (string, []Finding) {
	if s == "" {
		return "", nil
	}
	findings := r.findAll(s)
	if len(findings) == 0 {
		return s, nil
	}
	findings = mergeFindings(findings)

	var b strings.Builder
	b.Grow(len(s))
	cursor := 0
	for _, f := range findings {
		if f.Start > cursor {
			b.WriteString(s[cursor:f.Start])
		}
		b.WriteString(placeholder(f.Kind))
		cursor = f.End
	}
	if cursor < len(s) {
		b.WriteString(s[cursor:])
	}
	return b.String(), findings
}

// Detect reports the findings without mutating s. Useful for audit / dry-run
// modes where the caller wants to know whether a string would be redacted
// without paying the build-string cost.
func (r *Redactor) Detect(s string) []Finding {
	if s == "" {
		return nil
	}
	return mergeFindings(r.findAll(s))
}

func (r *Redactor) findAll(s string) []Finding {
	var findings []Finding
	for _, rule := range r.rules {
		for _, m := range rule.Pattern.FindAllStringIndex(s, -1) {
			start, end := m[0], m[1]
			// When the rule defines a capture group (e.g. AWS secret,
			// Bearer token, where the secret is preceded by a label),
			// redact only the captured material.
			if sub := rule.Pattern.FindStringSubmatchIndex(s[start:end]); len(sub) >= 4 && sub[2] >= 0 {
				start += sub[2]
				end = m[0] + sub[3]
			}
			findings = append(findings, Finding{
				Kind:  rule.Kind,
				Start: start,
				End:   end,
				Match: s[start:end],
			})
		}
	}
	if r.entropyEnabled {
		findings = append(findings, r.entropyFindings(s, findings)...)
	}
	return findings
}

func (r *Redactor) entropyFindings(s string, existing []Finding) []Finding {
	var out []Finding
	for _, tok := range candidateTokens(s, r.minTokenLen) {
		// Skip if the token sits inside an existing match. Entropy is a
		// fallback, not a duplicate label.
		idx := strings.Index(s, tok)
		if idx < 0 {
			continue
		}
		if covered(existing, idx, idx+len(tok)) {
			continue
		}
		if shannonEntropy(tok) < r.entropyThreshold {
			continue
		}
		out = append(out, Finding{
			Kind:  KindHighEntropy,
			Start: idx,
			End:   idx + len(tok),
			Match: tok,
		})
	}
	return out
}

func covered(fs []Finding, start, end int) bool {
	for _, f := range fs {
		if start >= f.Start && end <= f.End {
			return true
		}
		if start < f.End && end > f.Start {
			return true
		}
	}
	return false
}

// mergeFindings normalises the slice: sorts by start offset, drops zero-
// length entries, and resolves overlaps by keeping the earliest, longest
// match. The result is non-overlapping and ordered.
func mergeFindings(fs []Finding) []Finding {
	if len(fs) == 0 {
		return nil
	}
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Start != fs[j].Start {
			return fs[i].Start < fs[j].Start
		}
		// Longer match first — overlap resolution prefers the wider span.
		return (fs[i].End - fs[i].Start) > (fs[j].End - fs[j].Start)
	})
	out := make([]Finding, 0, len(fs))
	for _, f := range fs {
		if f.End <= f.Start {
			continue
		}
		if len(out) > 0 && f.Start < out[len(out)-1].End {
			continue
		}
		out = append(out, f)
	}
	return out
}
