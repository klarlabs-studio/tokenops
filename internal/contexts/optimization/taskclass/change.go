package taskclass

import "strings"

// ChangeClass is what a finished change was: a bug fixed, a feature added,
// code reshaped. Kind classifies an instruction before the work; a change
// class classifies its result, which is what unit costs are reported by.
//
// It is read from the Conventional Commits type a commit or pull request
// subject opens with. That is deliberately the whole rule: the
// repositories already label their changes this way, and a class the
// operator can predict from the subject is worth more than one inferred
// from the session.
type ChangeClass string

// The classes, in the order a report lists them.
const (
	ChangeFeature  ChangeClass = "feature"
	ChangeBugfix   ChangeClass = "bugfix"
	ChangeRefactor ChangeClass = "refactor"
	ChangeTest     ChangeClass = "test"
	ChangeDocs     ChangeClass = "docs"
	ChangeChore    ChangeClass = "chore"
	// ChangeOther is a subject that names no type. It is reported, not
	// guessed at.
	ChangeOther ChangeClass = "other"
)

// ChangeClasses lists every class in report order, Other last.
func ChangeClasses() []ChangeClass {
	return []ChangeClass{ChangeFeature, ChangeBugfix, ChangeRefactor, ChangeTest, ChangeDocs, ChangeChore, ChangeOther}
}

var changeTypes = map[string]ChangeClass{
	"feat": ChangeFeature, "fix": ChangeBugfix,
	"refactor": ChangeRefactor, "perf": ChangeRefactor,
	"test": ChangeTest, "docs": ChangeDocs,
	"chore": ChangeChore, "ci": ChangeChore, "build": ChangeChore, "style": ChangeChore, "revert": ChangeChore,
}

// ChangeClassOf reads a commit or pull request subject: "type(scope)!:"
// or "type:" at its start names the class.
func ChangeClassOf(subject string) ChangeClass {
	s := strings.ToLower(strings.TrimSpace(subject))
	i := strings.IndexAny(s, "(!:")
	if i <= 0 {
		return ChangeOther
	}
	class, ok := changeTypes[s[:i]]
	if !ok {
		return ChangeOther
	}
	rest := s[i:]
	if strings.HasPrefix(rest, "(") {
		end := strings.IndexByte(rest, ')')
		if end < 0 {
			return ChangeOther
		}
		rest = rest[end+1:]
	}
	rest = strings.TrimPrefix(rest, "!")
	if !strings.HasPrefix(rest, ":") {
		return ChangeOther
	}
	return class
}
