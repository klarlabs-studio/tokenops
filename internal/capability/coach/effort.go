package coach

import (
	"fmt"
	"regexp"
)

// The coach suggests a lower default effort where the developer's own
// instructions show one would do (agentdx.SuggestEffort). It advises only:
// it never writes the setting, so the operator's own choice stands until
// they change it. Where a client has a settings key for the default
// effort, the advice names the exact change; the keys below were checked
// against each client's documentation on 2026-11-17.
//
//   - Claude Code (v2.1.251 or later): `modelSettings.<model>.effortLevel`
//     in ~/.claude/settings.json, one of low, medium, high or xhigh, kept
//     per model. `/effort <level>` in a session on that model saves the
//     same entry. The top-level `effortLevel` no longer applies to newer
//     models from user settings, so it is not suggested.
//   - Codex: top-level `model_reasoning_effort` in ~/.codex/config.toml. It
//     applies to every model Codex runs; no per-model key exists.
//
// opencode records the effort as each message's variant; no documented
// default was verified, so it gets the advice without a setting.

// claudeEffortLevels are the levels Claude Code saves per model.
var claudeEffortLevels = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true}

// safeSettingWord is what a model or level may look like to be quoted
// into a setting: transcripts are the operator's files, not TokenOps'.
var safeSettingWord = regexp.MustCompile(`^[A-Za-z0-9._:/\[\]-]{1,128}$`)

// EffortChange is the change that makes level the default effort for model
// in client, in the client's own settings; ok is false where TokenOps knows
// of no such setting, and the suggestion is advice alone.
func EffortChange(client, model, level string) (string, bool) {
	if !safeSettingWord.MatchString(model) || !safeSettingWord.MatchString(level) {
		return "", false
	}
	switch client {
	case "claude-code":
		if !claudeEffortLevels[level] {
			return "", false
		}
		return fmt.Sprintf(`in ~/.claude/settings.json set "modelSettings": {"%s": {"effortLevel": "%s"}}, or run /effort %s in a session on %s (Claude Code 2.1.251 or later)`,
			model, level, level, model), true
	case "codex":
		return fmt.Sprintf(`in ~/.codex/config.toml set model_reasoning_effort = "%s" (Codex applies it to every model it runs)`, level), true
	}
	return "", false
}
