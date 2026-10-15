// Package allocation files work under the operator's own allocation tags —
// project, client and cost centre — by rules that match where the work ran:
// its repository, its working directory, its branch or the harness it ran
// in. FinOps tools call such rules virtual tags: nothing is tagged at the
// source, the tags are derived after the fact and can be changed and
// re-derived at will.
//
// A tag is finalized per key: the first rule, in the order written, that
// matches the work and sets the key gives its value. A broad rule further
// down can so fill a key a narrower rule above left unset.
//
// The package is pure: the caller resolves a session's directory,
// repository names, branch and harness, and asks Evaluate.
package allocation

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Key names an allocation dimension.
type Key string

// The allocation keys.
const (
	Project    Key = "project"
	Client     Key = "client"
	CostCenter Key = "cost_center"
)

// Keys lists every key, in the order surfaces show them.
var Keys = []Key{Project, Client, CostCenter}

// ParseKey reads a key, case-insensitively; "cost-center" is cost_center.
func ParseKey(s string) (Key, error) {
	k := Key(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "-", "_"))
	for _, known := range Keys {
		if k == known {
			return k, nil
		}
	}
	return "", fmt.Errorf("allocation key must be project, client or cost_center, got %q", s)
}

// MaxValueLen bounds a tag value, in characters.
const MaxValueLen = 64

// Tags are the finalized values of each key; an empty value is unset.
type Tags struct {
	Project    string `json:"project,omitempty"`
	Client     string `json:"client,omitempty"`
	CostCenter string `json:"cost_center,omitempty"`
}

// Get is k's value.
func (t Tags) Get(k Key) string {
	switch k {
	case Project:
		return t.Project
	case Client:
		return t.Client
	case CostCenter:
		return t.CostCenter
	}
	return ""
}

func (t *Tags) set(k Key, v string) {
	switch k {
	case Project:
		t.Project = v
	case Client:
		t.Client = v
	case CostCenter:
		t.CostCenter = v
	}
}

// Empty reports whether no key is set.
func (t Tags) Empty() bool { return t == Tags{} }

// Map is the set keys and their values, nil when none is set.
func (t Tags) Map() map[string]string {
	var out map[string]string
	for _, k := range Keys {
		if v := t.Get(k); v != "" {
			if out == nil {
				out = map[string]string{}
			}
			out[string(k)] = v
		}
	}
	return out
}

// Match is what a rule matches work by. Every criterion set must hold; one
// at least must be set.
type Match struct {
	// Repo is a glob over the repository's names: "owner/name" from its
	// origin remote and its top-level directory's name ("acme/*",
	// "storefront"). Compared case-insensitively.
	Repo string
	// Dir is an absolute glob over the working directory; work in a
	// directory under one it matches matches too ("/src/clients/acme",
	// "/src/*/infra"). A trailing "/**" changes nothing.
	Dir string
	// BranchPrefix matches the branch the client recorded.
	BranchPrefix string
	// Harness is the client the work ran in: claude-code, codex,
	// opencode, cursor and so on. Compared case-insensitively.
	Harness string
}

func (m Match) empty() bool { return m == Match{} }

// Rule tags the work it matches.
type Rule struct {
	Match Match
	Tags  Tags
}

// Subject is what a rule is matched against: where one session ran.
type Subject struct {
	// Repos are the names its repository goes by; empty outside one.
	Repos []string
	// Dir is its working directory, empty when no client recorded it.
	Dir string
	// Branch is the branch the client recorded, empty when none.
	Branch  string
	Harness string
}

// Evaluate finalizes s's tags: each key's value is the first matching
// rule's that sets it.
func Evaluate(rules []Rule, s Subject) Tags {
	var out Tags
	for _, r := range rules {
		if !r.matches(s) {
			continue
		}
		for _, k := range Keys {
			if out.Get(k) == "" {
				out.set(k, r.Tags.Get(k))
			}
		}
	}
	return out
}

func (r Rule) matches(s Subject) bool {
	m := r.Match
	if m.empty() {
		return false
	}
	if m.Harness != "" && !strings.EqualFold(m.Harness, s.Harness) {
		return false
	}
	if m.BranchPrefix != "" && (s.Branch == "" || !strings.HasPrefix(s.Branch, m.BranchPrefix)) {
		return false
	}
	if m.Dir != "" && !matchDir(m.Dir, s.Dir) {
		return false
	}
	if m.Repo != "" && !matchRepo(m.Repo, s.Repos) {
		return false
	}
	return true
}

func matchRepo(pattern string, names []string) bool {
	pattern = strings.ToLower(pattern)
	for _, n := range names {
		if ok, _ := path.Match(pattern, strings.ToLower(n)); ok && n != "" {
			return true
		}
	}
	return false
}

// matchDir reports whether dir, or a directory it lies under, matches
// pattern.
func matchDir(pattern, dir string) bool {
	if dir == "" {
		return false
	}
	pattern = filepath.Clean(strings.TrimSuffix(pattern, string(filepath.Separator)+"**"))
	for d := filepath.Clean(dir); ; {
		if ok, _ := filepath.Match(pattern, d); ok {
			return true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
}

// Validate checks every rule, naming the first that is wrong by its
// position (counted from 1).
func Validate(rules []Rule) error {
	for i, r := range rules {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("allocation rule %d: %w", i+1, err)
		}
	}
	return nil
}

// Validate checks one rule.
func (r Rule) Validate() error {
	m := r.Match
	if m.empty() {
		return errors.New("match needs at least one of repo, dir, branch_prefix or harness")
	}
	if m.Repo != "" {
		if _, err := path.Match(m.Repo, ""); err != nil {
			return fmt.Errorf("repo %q is not a valid glob", m.Repo)
		}
	}
	if m.Dir != "" {
		if _, err := filepath.Match(m.Dir, ""); err != nil {
			return fmt.Errorf("dir %q is not a valid glob", m.Dir)
		}
		if !filepath.IsAbs(m.Dir) {
			return fmt.Errorf("dir %q must be an absolute path (or start with ~/)", m.Dir)
		}
	}
	if r.Tags.Empty() {
		return errors.New("a rule needs at least one tag: project, client or cost_center")
	}
	for _, k := range Keys {
		if err := validValue(r.Tags.Get(k)); err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
	}
	return nil
}

// validValue checks a tag value; empty is unset and valid.
func validValue(v string) error {
	if v == "" {
		return nil
	}
	if utf8.RuneCountInString(v) > MaxValueLen {
		return fmt.Errorf("value is longer than %d characters", MaxValueLen)
	}
	if strings.TrimSpace(v) != v {
		return fmt.Errorf("value %q starts or ends with a space", v)
	}
	for _, c := range v {
		if unicode.IsControl(c) {
			return errors.New("value holds a control character")
		}
	}
	return nil
}

// Scope narrows something, such as a budget, to work carrying one tag.
type Scope struct {
	Key   Key
	Value string
}

// ParseScope reads "key=value".
func ParseScope(s string) (Scope, error) {
	k, v, ok := strings.Cut(s, "=")
	if !ok {
		return Scope{}, fmt.Errorf("a tag scope is key=value, such as project=storefront; got %q", s)
	}
	key, err := ParseKey(k)
	if err != nil {
		return Scope{}, err
	}
	if v == "" {
		return Scope{}, fmt.Errorf("tag scope %q has no value", s)
	}
	if err := validValue(v); err != nil {
		return Scope{}, err
	}
	return Scope{Key: key, Value: v}, nil
}

// String is the scope as ParseScope reads it.
func (s Scope) String() string { return string(s.Key) + "=" + s.Value }

// Holds reports whether t carries the scope's tag.
func (s Scope) Holds(t Tags) bool { return s.Value != "" && t.Get(s.Key) == s.Value }
