// Package routers answers which harness has an external router choosing
// its models (ADR 0009 §5): FireRouter in front of Claude Code, OpenRouter
// Auto in opencode. Where one decides, TokenOps does not choose the model
// for that harness; the coach's models power and the routing advice stand
// down there and say why.
package routers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"go.klarlabs.de/tokenops/internal/contexts/spend/biller"
	"go.klarlabs.de/tokenops/internal/infra/claudesettings"
	"go.klarlabs.de/tokenops/internal/infra/codexsettings"
	"go.klarlabs.de/tokenops/internal/infra/harnesskeys"
)

// Harness names, as the route guard and the coach use them.
const (
	ClaudeCode = "claude-code"
	Codex      = "codex"
	Opencode   = "opencode"
)

// InPath is a router a harness is configured to ask.
type InPath struct {
	Harness string        `json:"harness"`
	Router  biller.Router `json:"router"`
	// Setting is where the harness names it, Model the ID it asks for.
	Setting string `json:"setting"`
	Model   string `json:"model"`
}

// Sources are where each harness's model setting is read; zero values
// read the real ones.
type Sources struct {
	ClaudeModels func() map[string]string
	CodexModel   func() string
	OpencodeDir  string
}

func (s Sources) withDefaults() Sources {
	if s.ClaudeModels == nil {
		s.ClaudeModels = claudesettings.Models
	}
	if s.CodexModel == nil {
		s.CodexModel = codexsettings.ReadModel
	}
	if s.OpencodeDir == "" {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			if home, err := os.UserHomeDir(); err == nil {
				base = filepath.Join(home, ".config")
			}
		}
		s.OpencodeDir = filepath.Join(base, "opencode")
	}
	return s
}

// Detect lists every harness whose configured model is a router, at most
// one entry per harness.
func Detect(src Sources) []InPath {
	src = src.withDefaults()
	var out []InPath
	claude := src.ClaudeModels()
	settings := make([]string, 0, len(claude))
	for k := range claude {
		settings = append(settings, k)
	}
	sort.Strings(settings)
	for _, setting := range settings {
		if r, ok := biller.RouterFor(claude[setting]); ok {
			out = append(out, InPath{Harness: ClaudeCode, Router: r, Setting: "Claude Code settings " + setting, Model: claude[setting]})
			break
		}
	}
	if m := src.CodexModel(); m != "" {
		if r, ok := biller.RouterFor(m); ok {
			out = append(out, InPath{Harness: Codex, Router: r, Setting: "Codex config.toml model", Model: m})
		}
	}
	if m := opencodeModel(src.OpencodeDir); m != "" {
		if r, ok := biller.RouterFor(m); ok {
			out = append(out, InPath{Harness: Opencode, Router: r, Setting: "opencode config model", Model: m})
		}
	}
	return out
}

// For returns the router in harness's path, if any.
func For(src Sources, harness string) (InPath, bool) {
	for _, r := range Detect(src) {
		if r.Harness == harness {
			return r, true
		}
	}
	return InPath{}, false
}

// opencodeModel is the model opencode's config asks for.
func opencodeModel(dir string) string {
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		b, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // opencode's own config path
		if err != nil {
			continue
		}
		var cfg struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(harnesskeys.StripJSONC(b), &cfg) == nil {
			return cfg.Model
		}
	}
	return ""
}

// StandDown is the sentence a surface shows for a router in the path.
func (r InPath) StandDown() string {
	return r.Router.Display + " chooses the model in " + harnessName(r.Harness) +
		" (" + r.Setting + " = " + r.Model + "), so TokenOps does not move work to another model there"
}

func harnessName(h string) string {
	switch h {
	case ClaudeCode:
		return "Claude Code"
	case Codex:
		return "Codex"
	case Opencode:
		return "opencode"
	}
	return h
}

// Decides reports whether an external router chooses the model for the
// calling harness: the model it asked for names one, or, for Claude Code,
// whose hook payload carries no model, its settings do. Two routers for
// one turn cannot be explained (ADR 0009 §5), so TokenOps then stands
// down.
func Decides(src Sources, requested string, claudeCode bool) bool {
	if _, ok := biller.RouterFor(requested); ok {
		return true
	}
	if !claudeCode {
		return false
	}
	_, ok := For(src, ClaudeCode)
	return ok
}

// RouterNamedBy reports the router a requested model ID names, if any.
func RouterNamedBy(model string) (biller.Router, bool) { return biller.RouterFor(model) }
