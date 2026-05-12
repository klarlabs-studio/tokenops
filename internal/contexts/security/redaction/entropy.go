package redaction

import (
	"math"
	"strings"
)

// shannonEntropy returns the Shannon entropy in bits/char of s. High entropy
// suggests random material — API keys, tokens, hashes — rather than prose.
// Returns 0 for empty input.
func shannonEntropy(s string) float64 {
	if s == "" {
		return 0
	}
	counts := make(map[rune]int, len(s))
	for _, r := range s {
		counts[r]++
	}
	total := float64(len(s))
	var h float64
	for _, c := range counts {
		p := float64(c) / total
		h -= p * math.Log2(p)
	}
	return h
}

// candidateTokens splits s on whitespace and common separator characters
// used in JSON/headers (= : , ; "). Tokens shorter than minLen are dropped.
func candidateTokens(s string, minLen int) []string {
	if minLen <= 0 {
		minLen = 20
	}
	splitter := func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', '"', ',', ';', ':', '=', '(', ')', '[', ']', '{', '}':
			return true
		}
		return false
	}
	var out []string
	for _, tok := range strings.FieldsFunc(s, splitter) {
		if len(tok) >= minLen {
			out = append(out, tok)
		}
	}
	return out
}
