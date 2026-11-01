package prefixlint

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// hookSite is where a hook command is configured.
type hookSite struct {
	path string
	line int
	hook string
}

// settingsFiles are Claude Code's settings files that configure hooks:
// managed, the user's, then the project's.
func (l *linter) settingsFiles() []string {
	var out []string
	if l.src.ManagedSettings != "" {
		out = append(out, l.src.ManagedSettings)
	}
	if l.src.ClaudeDir != "" {
		out = append(out, filepath.Join(l.src.ClaudeDir, "settings.json"), filepath.Join(l.src.ClaudeDir, "settings.local.json"))
	}
	if l.src.ProjectDir != "" {
		dir := filepath.Clean(l.src.ProjectDir)
		out = append(out, filepath.Join(dir, ".claude", "settings.json"), filepath.Join(dir, ".claude", "settings.local.json"))
	}
	return out
}

// contextEvents are the hook events whose output enters the context.
var contextEvents = []string{EventSessionStart, EventUserPromptSubmit}

// hooks lints the SessionStart and UserPromptSubmit commands in one
// settings file. Only the command text is read; nothing is run.
func (l *linter) hooks(path string) {
	text, ok := l.readOnce(path)
	if !ok {
		return
	}
	var s struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal([]byte(text), &s) != nil {
		return
	}
	if l.hookPaths == nil {
		l.hookPaths = map[string]hookSite{}
	}
	for _, event := range contextEvents {
		for _, group := range s.Hooks[event] {
			for _, h := range group.Hooks {
				if h.Type != "" && h.Type != "command" || strings.TrimSpace(h.Command) == "" {
					continue
				}
				site := hookSite{path: path, line: lineOf(text, h.Command), hook: event}
				if _, known := l.hookPaths[h.Command]; !known {
					l.hookPaths[h.Command] = site
				}
				for _, v := range volatileCommands(h.Command) {
					l.findings = append(l.findings, hookFinding(site, program(h.Command), v))
				}
			}
		}
	}
}

// lineOf is the 1-based line on which command appears, JSON-encoded as
// the settings file holds it, or 0.
func lineOf(text, command string) int {
	for _, escapeHTML := range []bool{false, true} {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(escapeHTML)
		if enc.Encode(command) != nil {
			return 0
		}
		if i := strings.Index(text, strings.TrimSpace(b.String())); i >= 0 {
			return strings.Count(text[:i], "\n") + 1
		}
	}
	return 0
}

// volatile is one thing in a command that prints a changing value.
type volatile struct {
	kind  string
	token string
}

// shellSplit cuts a command at the points a new command starts.
var shellSplit = regexp.MustCompile("\\$\\(|`|;|&&|\\|\\||\\||\\n|\\(|\\)")

// stdoutRedirect is a segment whose standard output goes to a file, not
// the context: "> f", ">> f", "1> f", "&> f"; "2> f" leaves stdout alone.
var stdoutRedirect = regexp.MustCompile(`(?:^|[^0-9&>])>{1,2}\s*[^\s&]|\b1>{1,2}|&>`)

// gitState are git subcommands that print what changes between sessions.
var gitState = map[string]bool{"log": true, "status": true, "rev-parse": true, "branch": true, "describe": true, "show": true, "diff": true, "shortlog": true, "reflog": true}

// ghRemote are gh subcommands that print what is on the forge now: open
// pull requests, issues, runs.
var ghRemote = map[string]bool{"pr": true, "issue": true, "run": true, "api": true, "search": true, "release": true, "status": true, "workflow": true}

// ghSubcommand skips gh's global options ("-R owner/repo").
func ghSubcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-R" || a == "--repo" || a == "--hostname":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a
		}
	}
	return ""
}

// clockCode are inline snippets that read the clock or make an id.
var clockCode = []volatile{
	{KindClock, "datetime.now"}, {KindClock, "datetime.utcnow"}, {KindClock, "time.time()"},
	{KindClock, "Date.now("}, {KindClock, "new Date("}, {KindClock, "time.Now()"},
	{KindClock, "$EPOCHSECONDS"}, {KindClock, "$EPOCHREALTIME"}, {KindClock, "${EPOCHSECONDS}"},
	{KindRandom, "$RANDOM"}, {KindRandom, "${RANDOM}"}, {KindRandom, "$SRANDOM"},
	{KindRandom, "uuid.uuid4"}, {KindRandom, "crypto.randomUUID"},
}

// volatileCommands lists what in command prints a changing value: the
// clock (date), an id (uuidgen) or repository state (git status, git log).
// A segment that sends its output to a file is left out.
func volatileCommands(command string) []volatile {
	var out []volatile
	for _, seg := range shellSplit.Split(command, -1) {
		if stdoutRedirect.MatchString(seg) {
			continue
		}
		words := commandWords(seg)
		if len(words) == 0 {
			continue
		}
		switch filepath.Base(words[0]) {
		case "sh", "bash", "zsh":
			// sh -c '…' runs its quoted script: read that instead.
			if len(words) > 2 && words[1] == "-c" {
				script := strings.Join(words[2:], " ")
				out = append(out, volatileCommands(strings.Trim(script, `"'`))...)
			}
		case "date", "gdate":
			out = append(out, volatile{KindClock, "date"})
		case "uptime":
			out = append(out, volatile{KindClock, "uptime"})
		case "uuidgen":
			out = append(out, volatile{KindRandom, "uuidgen"})
		case "git":
			if sub := gitSubcommand(words[1:]); gitState[sub] {
				out = append(out, volatile{KindGit, "git " + sub})
			}
		case "curl", "wget", "http", "https", "xh":
			out = append(out, volatile{KindRemote, filepath.Base(words[0])})
		case "gh":
			if sub := ghSubcommand(words[1:]); ghRemote[sub] {
				out = append(out, volatile{KindRemote, "gh " + sub})
			}
		case "ps", "df", "free", "who", "vm_stat", "iostat":
			out = append(out, volatile{KindSystem, filepath.Base(words[0])})
		}
	}
	for _, c := range clockCode {
		if strings.Contains(command, c.token) {
			out = append(out, c)
		}
	}
	// One finding per thing a command prints, however often it prints it.
	seen := map[volatile]bool{}
	uniq := out[:0]
	for _, v := range out {
		if !seen[v] {
			seen[v] = true
			uniq = append(uniq, v)
		}
	}
	return uniq
}

// commandWords are a segment's words with leading variable assignments
// and wrappers (env, exec, command, sudo) removed.
func commandWords(seg string) []string {
	words := strings.Fields(strings.Trim(strings.TrimSpace(seg), `"'{}`))
	for len(words) > 0 {
		w := words[0]
		if w == "env" || w == "exec" || w == "command" || w == "sudo" || w == "time" || w == "!" ||
			(strings.Contains(w, "=") && !strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "=")) {
			words = words[1:]
			continue
		}
		break
	}
	return words
}

// gitSubcommand skips git's global options ("-C dir", "--no-pager").
func gitSubcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a
		}
	}
	return ""
}

// program is a command's program name, never its arguments: the base
// name of its first word.
func program(command string) string {
	words := commandWords(shellSplit.Split(command, 2)[0])
	if len(words) == 0 {
		return ""
	}
	return truncate(filepath.Base(words[0]))
}

func hookFinding(site hookSite, prog string, v volatile) Finding {
	f := Finding{
		Source: SourceHook, Clients: []string{ClientClaudeCode},
		Path: site.path, Line: site.line, Hook: site.hook, Command: prog,
		Kind: v.kind, Token: v.token,
	}
	if site.hook == EventSessionStart {
		f.Severity = SeverityHigh
		f.Why = "A SessionStart hook's output is added to the context at the start of every session, after the system prompt and before your first message. " +
			"`" + v.token + "` prints something different each session, so no session reuses another's cached prefix past this point."
	} else {
		f.Severity = SeverityMedium
		f.Why = "A UserPromptSubmit hook's output is added on every turn. `" + v.token + "` makes it differ each time, so each copy is new input that is never served from the cache " +
			"(earlier turns stay cached: the output is appended, not placed before them)."
	}
	switch v.kind {
	case KindClock:
		f.Fix = "Drop it from " + site.hook + "; let the agent run `date` when it needs the date or time."
	case KindGit, KindRemote, KindSystem:
		f.Fix = "Drop it from " + site.hook + "; let the agent run `" + v.token + "` when it needs it."
	default:
		f.Fix = "Drop the generated value from " + site.hook + "'s output; if the agent needs an id, let it make one when it needs it."
	}
	return f
}
