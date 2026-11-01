package agentdx

import (
	"encoding/json"
	"strings"
)

// readOnlyPrograms are the commands that only read, when run on their
// own: their output depends on the working tree and nothing else, so the
// same command with nothing changed since gives the same answer again.
var readOnlyPrograms = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "grep": true, "rg": true,
	"wc": true, "pwd": true, "tree": true, "find": true, "stat": true, "file": true,
}

// readOnlyGit are the git subcommands that only read.
var readOnlyGit = map[string]bool{
	"status": true, "diff": true, "log": true, "show": true, "blame": true,
	"rev-parse": true, "ls-files": true,
}

// shellOperators chain, pipe or redirect a command, after which nothing
// can be said about what it does.
const shellOperators = "|&;<>`$("

// readOnlyCommand reports whether a Bash command only reads, and its
// normalised form for recognising it run again. Conservative: a command
// that chains, pipes, redirects or substitutes is never read-only, nor is
// find with an action that changes things.
func readOnlyCommand(cmd string) (string, bool) {
	if strings.ContainsAny(cmd, shellOperators) {
		return "", false
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return "", false
	}
	switch prog := fields[0]; {
	case prog == "git":
		if len(fields) < 2 || !readOnlyGit[fields[1]] {
			return "", false
		}
	case prog == "find":
		for _, f := range fields[1:] {
			if f == "-delete" || strings.HasPrefix(f, "-exec") || strings.HasPrefix(f, "-ok") {
				return "", false
			}
		}
	case !readOnlyPrograms[prog]:
		return "", false
	}
	return strings.Join(fields, " "), true
}

// bashCommand reads the command a Bash tool call ran.
func bashCommand(input json.RawMessage) string {
	var in struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(input, &in)
	return in.Command
}

// repeatedCommands counts, per session in time order, the read-only
// commands and those run again with nothing changed since: no edit and no
// command that may change things in between. Each session is its own
// context, so a repeat across sessions is not one.
func repeatedCommands(records []Record) (readOnly, repeated int) {
	seen := map[string]map[string]bool{}
	for _, r := range sortedByTime(records) {
		if r.Kind != KindToolUse {
			continue
		}
		s := seen[r.SessionID]
		switch {
		case r.Command != "":
			readOnly++
			if s[r.Command] {
				repeated++
				continue
			}
			if s == nil {
				s = map[string]bool{}
				seen[r.SessionID] = s
			}
			s[r.Command] = true
		case r.Mutates:
			delete(seen, r.SessionID)
		}
	}
	return readOnly, repeated
}
