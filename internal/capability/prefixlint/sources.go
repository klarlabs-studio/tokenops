package prefixlint

import (
	"os"
	"path/filepath"

	"go.klarlabs.de/tokenops/internal/claudehome"
)

// Sources are the places the lint reads. Every path is explicit so a test
// can point it at a temporary home.
type Sources struct {
	// Home resolves "@~/" imports.
	Home string
	// ClaudeDir is Claude Code's user directory (~/.claude, or
	// $CLAUDE_CONFIG_DIR): CLAUDE.md and settings.json.
	ClaudeDir string
	// CodexDir is Codex's home (~/.codex, or $CODEX_HOME): AGENTS.md.
	CodexDir string
	// OpencodeDir is opencode's config directory
	// ($XDG_CONFIG_HOME/opencode): AGENTS.md.
	OpencodeDir string
	// ManagedSettings is Claude Code's managed settings file, if any.
	ManagedSettings string
	// ProjectDir is the project whose CLAUDE.md, AGENTS.md and
	// .claude/settings.json are read, with those of its parents up to the
	// repository root. Empty reads the user's files only.
	ProjectDir string
	// TranscriptRoot is Claude Code's projects directory, read for what
	// hooks printed. Empty skips it.
	TranscriptRoot string
	// MaxTranscripts caps how many transcripts, newest first, are read.
	MaxTranscripts int
}

// DefaultMaxTranscripts is how many recent transcripts are read for hook
// output when Sources does not say.
const DefaultMaxTranscripts = 20

// UserSources are the user's own files on this machine, with no project
// and no transcripts: what every session loads wherever it runs, cheap
// enough to read on each glance.
func UserSources() Sources {
	home, _ := os.UserHomeDir()
	s := Sources{Home: home}
	if home != "" {
		s.CodexDir = filepath.Join(home, ".codex")
		s.OpencodeDir = filepath.Join(home, ".config", "opencode")
	}
	s.ClaudeDir = claudehome.Resolve(os.Getenv, home)
	if d := os.Getenv("CODEX_HOME"); d != "" {
		s.CodexDir = d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		s.OpencodeDir = filepath.Join(d, "opencode")
	}
	s.ManagedSettings = filepath.Join(claudehome.ManagedDir, "managed-settings.json")
	return s
}

// ProjectSources are UserSources plus the project at dir and, unless
// transcriptRoot is "-", the transcripts under transcriptRoot (empty: the
// default ~/.claude/projects).
func ProjectSources(dir, transcriptRoot string) Sources {
	s := UserSources()
	s.ProjectDir = dir
	switch transcriptRoot {
	case "-":
	case "":
		if s.ClaudeDir != "" {
			s.TranscriptRoot = filepath.Join(s.ClaudeDir, "projects")
		}
	default:
		s.TranscriptRoot = transcriptRoot
	}
	return s
}
