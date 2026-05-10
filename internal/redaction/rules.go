// Package redaction detects secrets and PII in strings emitted by TokenOps.
// The detector runs before any non-local emission (OTLP, ClickHouse, cloud
// telemetry) so a leaked API key or bearer token never escapes the host.
//
// The package is intentionally conservative: false positives are cheaper
// than false negatives, and every match collapses the secret to a typed
// placeholder ("<redacted:openai_api_key>") rather than the raw value.
package redaction

import (
	"regexp"
)

// Kind classifies a redaction. Stable string identifiers are emitted in
// placeholders and reported by Findings so downstream tooling (audit log,
// config UI) can group by kind.
type Kind string

// Known kinds. New kinds append to this list; existing values are stable.
const (
	KindUnknown      Kind = "unknown"
	KindOpenAIKey    Kind = "openai_api_key"
	KindAnthropicKey Kind = "anthropic_api_key"
	KindGeminiKey    Kind = "gemini_api_key"
	KindAWSAccessKey Kind = "aws_access_key_id"
	KindAWSSecretKey Kind = "aws_secret_access_key"
	KindGitHubToken  Kind = "github_token"
	KindJWT          Kind = "jwt"
	KindBearerToken  Kind = "bearer_token"
	KindHighEntropy  Kind = "high_entropy"
	KindEmail        Kind = "email"
)

// Rule pairs a Kind with the regular expression that matches it. The
// expression must be safe to compile at process start; new rules are added
// to DefaultRules in the order detectors should run (most specific first).
type Rule struct {
	Kind    Kind
	Pattern *regexp.Regexp
}

// DefaultRules returns a fresh slice of the built-in rules. The slice is
// returned by value so callers can extend or filter without mutating
// package state.
func DefaultRules() []Rule {
	return []Rule{
		// Anthropic comes before OpenAI because both keys share an "sk-"
		// prefix; merge resolution keeps the first-listed rule on ties.
		{KindAnthropicKey, regexp.MustCompile(`sk-ant-(?:api\d{2}-)?[A-Za-z0-9_-]{32,}`)},
		{KindOpenAIKey, regexp.MustCompile(`sk-(?:proj-)?[A-Za-z0-9_-]{20,}`)},
		{KindGeminiKey, regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`)},
		{KindGitHubToken, regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`)},
		{KindAWSAccessKey, regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
		// AWS secret access keys are 40 base64-ish chars. We require an
		// "aws" hint nearby to avoid matching every random 40-char string.
		{KindAWSSecretKey, regexp.MustCompile(`(?i)aws[_-]?secret[_-]?access[_-]?key["':=\s]+([A-Za-z0-9/+=]{40})`)},
		{KindJWT, regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)},
		{KindBearerToken, regexp.MustCompile(`(?i)bearer\s+([A-Za-z0-9._\-+/=]{16,})`)},
		{KindEmail, regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)},
	}
}

// placeholder returns the string used to replace a secret of the given kind.
// Format: "<redacted:KIND>" — easy to grep, easy for downstream tools to
// recognise, never confused with valid secret material.
func placeholder(k Kind) string {
	if k == "" {
		k = KindUnknown
	}
	return "<redacted:" + string(k) + ">"
}
