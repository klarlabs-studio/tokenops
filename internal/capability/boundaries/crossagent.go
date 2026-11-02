package boundaries

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// agentCLIs maps each agent's command to the provider whose plan it
// spends. A tool call that starts one spends that plan, not only the
// session's own.
var agentCLIs = map[string]string{
	"codex":        "openai",
	"claude":       "anthropic",
	"gemini":       "gemini",
	"cursor-agent": "cursor",
	"copilot":      "github",
}

// harnessProvider is the plan each harness's own turns spend, which its
// pause point reads already. opencode's is any plan, so starting Claude
// Code from it still names the Claude plan.
var harnessProvider = map[string]string{HarnessClaudeCode: "anthropic", HarnessCodex: "openai"}

// commandSeparators split a shell command into the commands it runs.
var commandSeparators = regexp.MustCompile(`&&|\|\||[;|&\n]|\$\(|` + "`")

// launchers run the command after them; their own arguments are skipped.
var launchers = map[string]bool{"sudo": true, "env": true, "exec": true, "nohup": true, "time": true,
	"timeout": true, "npx": true, "bunx": true, "pnpx": true, "command": true}

// PlansStartedBy names the providers whose agent a shell command starts,
// other than the harness's own: `codex exec …` from Claude Code spends the
// Codex plan. Only the command word of each command counts, so a file
// named codex.md, a search for "codex" or `brew upgrade codex` start
// nothing. The command is read here and dropped; only provider names
// leave.
func PlansStartedBy(command, harness string) []string {
	var out []string
	for _, part := range commandSeparators.Split(command, -1) {
		provider, ok := agentCLIs[commandWord(part)]
		if !ok || provider == harnessProvider[harness] || slices.Contains(out, provider) {
			continue
		}
		out = append(out, provider)
	}
	return out
}

// commandWord is the program one command runs, past variable assignments
// and launchers, by its base name: /opt/homebrew/bin/codex and
// @openai/codex are both codex.
func commandWord(part string) string {
	fields := strings.Fields(part)
	for i := 0; i < len(fields); i++ {
		word := strings.Trim(fields[i], `"'(){}`)
		switch {
		case word == "" || strings.Contains(word, "=") && !strings.HasPrefix(word, "-"):
			continue
		case launchers[word]:
			// timeout's duration and env's or sudo's flags come next.
			for i+1 < len(fields) && (strings.HasPrefix(fields[i+1], "-") || word == "timeout" && isDuration(fields[i+1])) {
				i++
			}
			continue
		}
		return filepath.Base(word)
	}
	return ""
}

func isDuration(s string) bool {
	return strings.TrimRight(s, "0123456789.smhd") == "" && s != ""
}
