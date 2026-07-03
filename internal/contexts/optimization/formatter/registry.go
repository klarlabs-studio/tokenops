// Package formatter registry: maps command tokens to Formatters and applies
// the per-command loss policy. It is the single entry point the CLI wrapper
// and the proxy optimizer both call, so loss configuration and formatter
// dispatch live in one place.
package formatter

import "strings"

// LossPolicy carries the per-command loss configuration. Default is the
// level applied to any command without an explicit override; Overrides maps
// a command token to its level. This mirrors the "configurable per command"
// policy: operators can dial individual noisy commands more aggressively
// without loosening the global default.
type LossPolicy struct {
	Default   LossLevel
	Overrides map[string]LossLevel
}

// LevelFor resolves the loss level for a command token, honouring an
// override when present.
func (p LossPolicy) LevelFor(command string) LossLevel {
	if p.Overrides != nil {
		if lvl, ok := p.Overrides[strings.ToLower(command)]; ok {
			return lvl
		}
	}
	return p.Default
}

// Registry holds the formatter set and the loss policy.
type Registry struct {
	formatters map[string]Formatter
	policy     LossPolicy
}

// aliaser is the optional interface a Formatter implements to register
// under additional command tokens (e.g. a package-manager formatter that
// handles "yarn" and "pnpm", or "pip" and "pip3"). Command() remains the
// canonical token; Aliases() returns the extra ones.
type aliaser interface {
	Aliases() []string
}

// NewRegistry constructs a Registry with the given policy and formatters.
// A nil Overrides map is tolerated. Later formatters for the same command
// token replace earlier ones. A formatter implementing aliaser is also
// registered under each of its alias tokens.
func NewRegistry(policy LossPolicy, formatters ...Formatter) *Registry {
	r := &Registry{
		formatters: make(map[string]Formatter, len(formatters)),
		policy:     policy,
	}
	for _, f := range formatters {
		r.formatters[strings.ToLower(f.Command())] = f
		if a, ok := f.(aliaser); ok {
			for _, alias := range a.Aliases() {
				r.formatters[strings.ToLower(alias)] = f
			}
		}
	}
	return r
}

// Formatter returns the formatter registered for command, or nil.
func (r *Registry) Formatter(command string) Formatter {
	return r.formatters[strings.ToLower(command)]
}

// Commands returns the registered command tokens, order unspecified.
func (r *Registry) Commands() []string {
	out := make([]string, 0, len(r.formatters))
	for c := range r.formatters {
		out = append(out, c)
	}
	return out
}

// Format looks up the formatter for the first token of argv, resolves its
// loss level from the policy, and compresses raw. When no formatter is
// registered for the command it falls back to the always-safe generic
// formatter at the policy level so unknown commands still shed pure noise.
// The returned bool reports whether a command-specific formatter handled
// the input (false = generic fallback), letting callers attribute savings.
func (r *Registry) Format(argv []string, raw []byte) (Result, bool) {
	command := firstToken(argv)
	level := r.policy.LevelFor(command)
	if f := r.formatters[strings.ToLower(command)]; f != nil {
		res, ok := f.Format(raw, level)
		if !ok {
			return rawPassthrough(raw, "declined"), true
		}
		return res, true
	}
	res, ok := generic.Format(raw, level)
	if !ok {
		return rawPassthrough(raw, "declined"), false
	}
	return res, false
}

// FormatSniff compresses raw when the producing command is unknown — the
// proxy tool-output plane, where only the text is available. It runs every
// registered command formatter and picks the one that recognises the
// content (i.e. did not fall back to the generic scrub) and yields the
// smallest critical-preserving output. When nothing recognises the content
// it applies the generic noise scrub. The returned command token names the
// winning formatter ("" for the generic fallback).
func (r *Registry) FormatSniff(raw []byte, level LossLevel) (Result, string) {
	best, _ := generic.Format(raw, level)
	bestCmd := ""
	for cmd, f := range r.formatters {
		// User config formatters carry no reliable content signature; they
		// only run when dispatched by their explicit command token, never
		// by content sniffing.
		if ex, ok := f.(interface{ sniffExclude() bool }); ok && ex.sniffExclude() {
			continue
		}
		res, ok := f.Format(raw, level)
		if !ok || !res.CriticalKept {
			continue
		}
		// A formatter that fell back to its generic scrub did not
		// recognise the content; it has no command-specific claim.
		if strings.Contains(res.Notes, "generic") {
			continue
		}
		if res.BytesAfter < best.BytesAfter {
			best, bestCmd = res, cmd
		}
	}
	return best, bestCmd
}

// firstToken returns the command token argv[0] carries, ignoring a leading
// path (so "/usr/bin/git" keys as "git").
func firstToken(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	tok := argv[0]
	if i := strings.LastIndexAny(tok, "/\\"); i >= 0 {
		tok = tok[i+1:]
	}
	return tok
}

// generic is the package-level always-safe formatter used as the fallback
// for unregistered commands.
var generic = NewGeneric()

// DefaultFormatters returns the built-in command formatter set. It is the
// single source of truth for the catalog so every plane (the `tokenops fmt`
// CLI, the proxy tool-output optimizer, the hook generator, the benchmark)
// compresses with the same formatters. Adding a formatter here enrolls it
// everywhere.
func DefaultFormatters() []Formatter {
	return []Formatter{
		NewGit(),
		NewGoTest(),
		NewNPM(),
		NewCargo(),
		NewPytest(),
		NewDocker(),
		NewKubectl(),
		NewTerraform(),
		NewPip(),
		NewTSC(),
		NewESLint(),
		NewYarn(),
		NewMake(),
		NewMvn(),
		NewGradle(),
		NewApt(),
		NewCurl(),
		NewGH(),
		NewJest(),
		NewVitest(),
		NewGolangciLint(),
		NewRuff(),
		NewBazel(),
		NewAnsible(),
		NewHelm(),
		NewDotnet(),
		NewAws(),
		NewGcloud(),
		NewAz(),
		NewPulumi(),
		NewRSpec(),
		NewPlaywright(),
		NewRubocop(),
		NewPrettier(),
		NewBiome(),
		NewUV(),
		NewBundle(),
		NewComposer(),
		NewSBT(),
		NewMix(),
		NewDNF(),
		NewBrew(),
		NewFlyway(),
		NewAlembic(),
		NewCMake(),
		NewNinja(),
		NewNomad(),
		NewPacker(),
		NewGem(),
		NewSwift(),
		NewNix(),
	}
}

// DefaultRegistry builds a Registry with the default formatter set under the
// given loss policy.
func DefaultRegistry(policy LossPolicy) *Registry {
	return NewRegistry(policy, DefaultFormatters()...)
}
