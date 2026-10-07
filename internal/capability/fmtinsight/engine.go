package fmtinsight

import (
	"fmt"
	"strings"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/fmtlearn"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/formatter"
	"go.klarlabs.de/tokenops/internal/contexts/security/redaction"
)

// The engine `tokenops fmt` compresses with: the formatter registry, the
// loss levels it compresses at, and the policy config sets them by.

// LossLevel is how much a formatter may drop: conservative drops noise
// only, balanced and aggressive drop more. Critical lines survive all.
type LossLevel = formatter.LossLevel

// The loss levels.
const (
	LossConservative = formatter.LossConservative
	LossBalanced     = formatter.LossBalanced
	LossAggressive   = formatter.LossAggressive
)

// ParseLossLevel reads a level's name; false for one it does not know.
func ParseLossLevel(s string) (LossLevel, bool) { return formatter.ParseLossLevel(s) }

// LossPolicy is the level for each command, and the default.
type LossPolicy = formatter.LossPolicy

// Registry picks the formatter for a command and compresses its output.
type Registry = formatter.Registry

// NewRegistry is a registry over formatters at policy's levels.
func NewRegistry(policy LossPolicy, formatters ...Formatter) *Registry {
	return formatter.NewRegistry(policy, formatters...)
}

// Policy maps cfg's level names onto a LossPolicy, with override, when
// set, replacing the default and every per-command level for the run: the
// operator asked for that level. The warning names each configured level
// that does not parse, which falls back to conservative.
func Policy(cfg config.CommandFmtConfig, override string) (LossPolicy, string) {
	var warns []string
	def, ok := formatter.ParseLossLevel(cfg.Default)
	if !ok && cfg.Default != "" {
		warns = append(warns, fmt.Sprintf("invalid command_fmt.default %q, using conservative", cfg.Default))
	}
	overrides := make(map[string]formatter.LossLevel, len(cfg.Overrides))
	for cmdTok, lvl := range cfg.Overrides {
		parsed, ok := formatter.ParseLossLevel(lvl)
		if !ok {
			warns = append(warns, fmt.Sprintf("invalid command_fmt.overrides[%s]=%q, using conservative", cmdTok, lvl))
		}
		overrides[strings.ToLower(cmdTok)] = parsed
	}
	if override != "" {
		lvl, ok := formatter.ParseLossLevel(override)
		if !ok {
			warns = append(warns, fmt.Sprintf("invalid --level %q, using conservative", override))
		}
		def = lvl
		overrides = nil
	}
	return formatter.LossPolicy{Default: def, Overrides: overrides}, strings.Join(warns, "; ")
}

// recoveryRedactor strips credentials from captured output.
//
// It runs the pattern rules only — no entropy fallback, no email rule.
// Recovery exists so an operator can read back exactly what a command
// said, and the entropy detector flags any random-looking 20-character
// token, which in build and test output is usually a hash or an id. A
// recovery file full of `<redacted:high_entropy>` would not be worth
// keeping. Known credential shapes are what must never rest on disk.
var recoveryRedactor = redaction.New(redaction.Config{Rules: secretRulesOnly()})

// secretRulesOnly is DefaultRules minus the email rule — the operator's
// own address in their own `git log` is not the exposure this guards.
func secretRulesOnly() []redaction.Rule {
	all := redaction.DefaultRules()
	kept := make([]redaction.Rule, 0, len(all))
	for _, r := range all {
		if r.Kind == redaction.KindEmail {
			continue
		}
		kept = append(kept, r)
	}
	return kept
}

// RedactForRecovery strips the credentials from s before it is kept as a
// recovery file.
func RedactForRecovery(s string) string {
	out, _ := recoveryRedactor.Redact(s)
	return out
}

// LearnRecord is one line of the learn index: a compressed run, or an
// access to a recovery file that says the compression lost something.
type LearnRecord = fmtlearn.Record

// Learn record kinds and sources.
const (
	RecordCompress = fmtlearn.RecordCompress
	RecordAccess   = fmtlearn.RecordAccess
	SourceWrapped  = fmtlearn.SourceWrapped
)
