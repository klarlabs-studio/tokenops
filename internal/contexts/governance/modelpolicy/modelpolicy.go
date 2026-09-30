// Package modelpolicy decides which models TokenOps may route work to.
//
// A person or a company may rule models out: a vendor they have no
// agreement with, a model too expensive for the team, one that has not
// passed review. Routing that saves money by moving work onto such a
// model is not a saving the owner wanted, so every path that picks a
// model — the proxy's rules and smart routing, the coach's advice, its
// subagent moves — asks this policy first.
//
// The policy is two lists of patterns. Deny always wins. A non-empty
// allow list admits only what it names; an empty one admits everything
// not denied. A company that wants the policy on every machine writes it
// into the configuration through its device management, like any other
// setting.
package modelpolicy

import (
	"fmt"
	"strings"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Policy is the allow and deny lists.
//
// A pattern is matched, ignoring case, against both the bare model id
// ("claude-opus-5") and the provider-qualified one
// ("anthropic/claude-opus-5"), so "openai/*" rules out a vendor and
// "*opus*" a family wherever it is served. "*" matches any run of
// characters, including "/"; "?" matches one.
type Policy struct {
	Allow []string
	Deny  []string
}

// Verdict is the policy's answer for one model, with the pattern that
// decided it.
type Verdict struct {
	Permitted bool
	// Reason names the rule, e.g. `denied by "*opus*"`, or is empty when
	// the model is permitted.
	Reason string
}

// Empty reports whether the policy constrains nothing.
func (p Policy) Empty() bool { return len(p.Allow) == 0 && len(p.Deny) == 0 }

// Check decides one model. An empty model is permitted: the policy
// cannot rule on a name it was not given, and callers that route never
// target an empty one.
func (p Policy) Check(provider eventschema.Provider, model string) Verdict {
	model = strings.TrimSpace(model)
	if model == "" {
		return Verdict{Permitted: true}
	}
	if pat, ok := firstMatch(p.Deny, provider, model); ok {
		return Verdict{Reason: fmt.Sprintf("denied by %q", pat)}
	}
	if len(p.Allow) == 0 {
		return Verdict{Permitted: true}
	}
	if _, ok := firstMatch(p.Allow, provider, model); ok {
		return Verdict{Permitted: true}
	}
	return Verdict{Reason: "not on the allow list"}
}

// Permits is Check reduced to its answer.
func (p Policy) Permits(provider eventschema.Provider, model string) bool {
	return p.Check(provider, model).Permitted
}

// Filter keeps the permitted models, in order.
func (p Policy) Filter(provider eventschema.Provider, models []string) []string {
	if p.Empty() {
		return models
	}
	out := make([]string, 0, len(models))
	for _, m := range models {
		if p.Permits(provider, m) {
			out = append(out, m)
		}
	}
	return out
}

// Validate rejects a pattern that can never mean what its author wanted.
func (p Policy) Validate() error {
	for _, list := range []struct {
		name     string
		patterns []string
	}{{"allow", p.Allow}, {"deny", p.Deny}} {
		for i, pat := range list.patterns {
			if strings.TrimSpace(pat) == "" {
				return fmt.Errorf("model_policy.%s[%d] is empty", list.name, i)
			}
		}
	}
	return nil
}

func firstMatch(patterns []string, provider eventschema.Provider, model string) (string, bool) {
	qualified := string(provider) + "/" + model
	for _, pat := range patterns {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		if Match(pat, model) || (provider != "" && Match(pat, qualified)) {
			return pat, true
		}
	}
	return "", false
}

// Match reports whether name matches the glob pattern, ignoring case.
// Unlike path.Match, "*" crosses "/", because model ids carry slashes
// ("anthropic/claude-opus-5" as opencode names it).
func Match(pattern, name string) bool {
	return match([]rune(strings.ToLower(pattern)), []rune(strings.ToLower(name)))
}

func match(p, s []rune) bool {
	// Iterative glob with single-star backtracking: linear in practice
	// and immune to the exponential blow-up of naive recursion.
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(s) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == s[si]):
			pi++
			si++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, si
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}
