// Package prefixlint finds content in an agent's always-loaded context
// that changes between sessions or turns, and so breaks the prompt cache.
//
// A provider caches a request's prefix: everything up to the first byte
// that differs from an earlier request. A timestamp near the start of the
// context makes every session's prefix differ there, so nothing after it
// is reused (Manus, "Context Engineering for AI Agents"; "Don't Break the
// Cache", arXiv 2601.06007). The fix is a stable, append-only prefix:
// volatile text moves after the stable part, or out of the prefix.
//
// The lint reads only what a harness loads into every session: Claude
// Code's CLAUDE.md files and the files they @import (one level), the
// SessionStart and UserPromptSubmit hooks in its settings, and the AGENTS.md
// files Codex and opencode load. A hook is never run: its command text is
// read, and, when asked, the output Claude Code's transcripts recorded for
// it. A finding carries the matched token only, never the text around it,
// and nothing is sent anywhere.
package prefixlint

import "fmt"

// Severities, most urgent first.
const (
	// SeverityHigh is text that differs in every session: a SessionStart
	// hook that prints the time, or whose recorded output changes.
	SeverityHigh = "high"
	// SeverityMedium is text that changes often: a per-turn hook's
	// volatile output, or a file line that a tool rewrites ("last
	// updated", a counter).
	SeverityMedium = "medium"
	// SeverityLow is text that changes only when someone edits the file:
	// a date written in CLAUDE.md.
	SeverityLow = "low"
)

// Sources of a finding.
const (
	// SourceFile is a CLAUDE.md, AGENTS.md or an imported file.
	SourceFile = "file"
	// SourceHook is a hook's command text in a settings file.
	SourceHook = "hook"
	// SourceHookOutput is what transcripts recorded a hook printing.
	SourceHookOutput = "hook-output"
)

// Kinds of volatile text.
const (
	KindTimestamp   = "timestamp"
	KindDate        = "date"
	KindTime        = "time"
	KindUUID        = "uuid"
	KindSessionID   = "session-id"
	KindGitSHA      = "git-sha"
	KindCounter     = "counter"
	KindToday       = "today"
	KindLastUpdated = "last-updated"
	// KindClock is a hook command that reads the clock (date, $EPOCHSECONDS).
	KindClock = "clock-command"
	// KindRandom is a hook command that makes a random value or an id.
	KindRandom = "random-command"
	// KindGit is a hook command that prints repository state.
	KindGit = "git-command"
	// KindOutputVaries is a hook whose recorded output differs between
	// sessions, though no single token in it gives the reason.
	KindOutputVaries = "output-varies"
)

// Hook events the lint reads.
const (
	EventSessionStart     = "SessionStart"
	EventUserPromptSubmit = "UserPromptSubmit"
)

// Finding is one volatile item in the always-loaded context.
type Finding struct {
	Severity string `json:"severity"`
	Source   string `json:"source"`
	// Clients are the harnesses that load the item: claude-code, codex,
	// opencode.
	Clients []string `json:"clients"`
	// Path is the file the item is in: the CLAUDE.md or AGENTS.md, or the
	// settings file that configures the hook. Empty for recorded output
	// whose hook is no longer configured.
	Path string `json:"path,omitempty"`
	// Line is the 1-based line, when known.
	Line int `json:"line,omitempty"`
	// Hook is the hook event (SessionStart, UserPromptSubmit) for a hook
	// or its output.
	Hook string `json:"hook,omitempty"`
	// Command is the hook command's program name, never its arguments.
	Command string `json:"command,omitempty"`
	Kind    string `json:"kind"`
	// Token is the matched text and nothing around it.
	Token string `json:"token"`
	Why   string `json:"why"`
	Fix   string `json:"fix"`
}

// Report is the lint's answer.
type Report struct {
	Findings []Finding `json:"findings"`
	// Read lists every file the lint read, so an empty report says what
	// it looked at.
	Read []string `json:"read"`
	// Transcripts is how many transcripts were read for hook output.
	Transcripts int `json:"transcripts_read"`
	High        int `json:"high"`
	Medium      int `json:"medium"`
	Low         int `json:"low"`
}

// HighFindings are the report's high-severity findings.
func (r Report) HighFindings() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity == SeverityHigh {
			out = append(out, f)
		}
	}
	return out
}

// Subject names an item without saying where it is: "SessionStart hook
// running `date`".
func (f Finding) Subject() string {
	switch f.Source {
	case SourceHook:
		return fmt.Sprintf("%s hook running `%s`", f.Hook, f.Token)
	case SourceHookOutput:
		return fmt.Sprintf("%s hook `%s` printing a %s", f.Hook, f.Command, f.Kind)
	}
	return fmt.Sprintf("%s in a context file", f.Kind)
}

// InPrefix reports whether the item sits in the cached prefix, so a change
// to it rebuilds the cache from there: a context file's line, or a
// SessionStart hook's output. A UserPromptSubmit hook's output is
// appended after the turns before it and leaves their cache alone.
func (f Finding) InPrefix() bool {
	return f.Source == SourceFile || f.Hook == EventSessionStart
}

// LoadedBy reports whether the harness client loads the item.
func (f Finding) LoadedBy(client string) bool {
	for _, c := range f.Clients {
		if c == client {
			return true
		}
	}
	return false
}
