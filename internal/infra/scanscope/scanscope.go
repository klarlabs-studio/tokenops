// Package scanscope decides which Claude Code transcripts describe the
// operator's own work.
//
// Claude Code writes every session under ~/.claude/projects/<project>/,
// where <project> is the directory the session ran in with its slashes
// turned into dashes. It keeps them all, including sessions run in
// scratch directories that no longer exist — a benchmark harness, a
// throwaway clone, a session started in /tmp.
//
// Counting those as the operator's history is not a rounding error. On
// one real machine 94% of a 7-day window was a simulation harness, and
// `tokenops dx` graded it: median 2 turns per instruction against the
// operator's real 20, zero tool calls against 9.4, a 19k context against
// 840k. Every figure was an A, and every figure was about a program.
//
// So scratch work is out of scope by default for the surfaces that claim
// to describe how the operator works — dx, story, the prompt coach — and
// callers that genuinely want it say so.
//
// Spend is deliberately not filtered this way. A simulated session still
// spent real tokens against a real plan, so the ledger and the headroom
// math are right to count it. What it did not do is tell anyone anything
// about how its operator works, because it had none.
package scanscope

import (
	"os"
	"path/filepath"
	"strings"
)

// scratchRoots are the directory prefixes whose contents are, by
// convention, throwaway. Written in Claude Code's encoded form: the
// project's path with "/" replaced by "-".
//
// macOS resolves /tmp and /var through /private, and a session records
// whichever spelling it was started with, so both are listed. Decoding
// the name back to a path is not possible — the encoding is lossy, since
// a directory whose name contains a dash is indistinguishable from a
// path separator — which is why this matches the encoded form directly
// rather than trying to reverse it.
var scratchRoots = []string{
	"-tmp-",
	"-private-tmp-",
	"-var-tmp-",
	"-private-var-tmp-",
	"-var-folders-",
	"-private-var-folders-",
}

// Ephemeral reports whether a project directory name describes work done
// in a scratch directory.
//
// The argument is the directory name Claude Code created, not a path on
// this machine: ~/.claude/projects is itself often under a temp root in
// tests, and the transcripts there are the fixture, not the noise.
func Ephemeral(projectDir string) bool {
	if projectDir == "" {
		return false
	}
	for _, p := range scratchRoots {
		if strings.HasPrefix(projectDir, p) {
			return true
		}
	}
	// Whatever this platform calls its temp directory, encoded the same
	// way. Covers Windows, and any macOS or Linux box that moved it.
	if tmp := encode(os.TempDir()); tmp != "" && strings.HasPrefix(projectDir, tmp) {
		return true
	}
	return false
}

// EphemeralPath is Ephemeral for a transcript file, whose parent
// directory carries the project name.
func EphemeralPath(transcriptPath string) bool {
	return Ephemeral(filepath.Base(filepath.Dir(transcriptPath)))
}

// Keep filters transcript paths down to the ones describing real work.
// The returned slice preserves input order.
func Keep(paths []string) []string {
	out := paths[:0:0]
	for _, p := range paths {
		if !EphemeralPath(p) {
			out = append(out, p)
		}
	}
	return out
}

// encode renders a filesystem path the way Claude Code names a project
// directory, with a trailing separator so a prefix match cannot pick up
// a sibling that merely starts with the same letters.
func encode(path string) string {
	path = strings.TrimSuffix(filepath.Clean(path), string(filepath.Separator))
	if path == "" {
		return ""
	}
	enc := strings.NewReplacer(string(filepath.Separator), "-", "/", "-", ":", "-").Replace(path)
	return enc + "-"
}
