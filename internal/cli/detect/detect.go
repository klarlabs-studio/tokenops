// Package detect sniffs installed AI clients on the host and infers
// which provider plans the operator is likely subscribed to. Outputs
// feed `tokenops init` so the operator types fewer plan names.
//
// Detection is intentionally heuristic — file-system + env-var
// fingerprints, no network calls and no credential reads. Confidence
// levels separate "we can bind without asking" from "we have to
// prompt". The package exposes pure functions so tests can stub the
// filesystem.
package detect

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

// Confidence describes how sure the detector is. The init wizard
// binds High without prompting, asks the user for Medium (offering a
// guess), and only mentions Low in a "you might also have…" note.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// Detection is one sniff result. Provider matches eventschema; Plan
// is the catalog name (or empty when the tier is ambiguous and needs
// a prompt). Evidence carries the path/env-var the detector keyed on
// so the wizard can show "based on ~/.claude" to the operator.
type Detection struct {
	Provider   string
	Plan       string
	Confidence Confidence
	Evidence   string
	Hint       string
}

// Env is the small interface the detector reads through. The real
// runtime supplies an OS-backed implementation; tests inject fakes.
type Env interface {
	HomeDir() (string, error)
	Getenv(key string) string
	Stat(path string) (fs.FileInfo, error)
}

// OSEnv is the production Env adapter — wraps the standard library.
type OSEnv struct{}

func (OSEnv) HomeDir() (string, error)              { return os.UserHomeDir() }
func (OSEnv) Getenv(key string) string              { return os.Getenv(key) }
func (OSEnv) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }

// Detect runs every known sniffer and returns the deduplicated set of
// detections sorted by (Confidence desc, Provider asc). When the
// caller provides a nil Env, OSEnv is used.
func Detect(env Env) []Detection {
	if env == nil {
		env = OSEnv{}
	}
	out := make([]Detection, 0, 8)
	out = append(out, detectClaudeCode(env)...)
	out = append(out, detectClaudeDesktop(env)...)
	out = append(out, detectCursor(env)...)
	out = append(out, detectChatGPTDesktop(env)...)
	out = append(out, detectAPIKeys(env)...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confidence != out[j].Confidence {
			return confidenceRank(out[i].Confidence) < confidenceRank(out[j].Confidence)
		}
		return out[i].Provider < out[j].Provider
	})
	return out
}

func confidenceRank(c Confidence) int {
	switch c {
	case ConfidenceHigh:
		return 0
	case ConfidenceMedium:
		return 1
	default:
		return 2
	}
}

func detectClaudeCode(env Env) []Detection {
	home, err := env.HomeDir()
	if err != nil {
		return nil
	}
	candidate := filepath.Join(home, ".claude")
	if info, err := env.Stat(candidate); err == nil && info.IsDir() {
		// Claude Code uses ~/.claude. Tier (Max 5x / Max 20x / Pro)
		// cannot be inferred without an API call — the wizard must
		// prompt.
		return []Detection{{
			Provider:   "anthropic",
			Plan:       "", // ambiguous tier; wizard prompts
			Confidence: ConfidenceMedium,
			Evidence:   candidate,
			Hint:       "Claude Code installed; pick your plan tier (Max 20x, Max 5x, Pro)",
		}}
	}
	return nil
}

func detectClaudeDesktop(env Env) []Detection {
	home, err := env.HomeDir()
	if err != nil {
		return nil
	}
	var candidate string
	switch runtime.GOOS {
	case "darwin":
		candidate = filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		candidate = filepath.Join(env.Getenv("APPDATA"), "Claude", "claude_desktop_config.json")
	default:
		candidate = filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
	if info, err := env.Stat(candidate); err == nil && !info.IsDir() {
		return []Detection{{
			Provider:   "anthropic",
			Plan:       "",
			Confidence: ConfidenceMedium,
			Evidence:   candidate,
			Hint:       "Claude Desktop installed; pick your plan tier",
		}}
	}
	return nil
}

func detectCursor(env Env) []Detection {
	home, err := env.HomeDir()
	if err != nil {
		return nil
	}
	for _, candidate := range []string{
		filepath.Join(home, "Library", "Application Support", "Cursor"),
		filepath.Join(home, ".cursor"),
		filepath.Join(home, ".config", "Cursor"),
	} {
		if info, err := env.Stat(candidate); err == nil && info.IsDir() {
			return []Detection{{
				Provider:   "cursor",
				Plan:       "",
				Confidence: ConfidenceMedium,
				Evidence:   candidate,
				Hint:       "Cursor installed; pick Pro or Business",
			}}
		}
	}
	return nil
}

func detectChatGPTDesktop(env Env) []Detection {
	home, err := env.HomeDir()
	if err != nil {
		return nil
	}
	for _, candidate := range []string{
		filepath.Join(home, "Library", "Application Support", "com.openai.chat"),
		filepath.Join(home, ".config", "openai"),
	} {
		if info, err := env.Stat(candidate); err == nil && info.IsDir() {
			return []Detection{{
				Provider:   "openai",
				Plan:       "",
				Confidence: ConfidenceMedium,
				Evidence:   candidate,
				Hint:       "ChatGPT desktop installed; pick Plus / Pro / Team",
			}}
		}
	}
	return nil
}

func detectAPIKeys(env Env) []Detection {
	var out []Detection
	for _, k := range []struct {
		envVar   string
		provider string
	}{
		{"ANTHROPIC_API_KEY", "anthropic"},
		{"OPENAI_API_KEY", "openai"},
		{"GEMINI_API_KEY", "gemini"},
	} {
		if env.Getenv(k.envVar) != "" {
			out = append(out, Detection{
				Provider:   k.provider,
				Plan:       "",
				Confidence: ConfidenceLow,
				Evidence:   k.envVar,
				Hint:       "raw API key — likely metered usage, no plan",
			})
		}
	}
	return out
}
