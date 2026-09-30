package compactlever

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// claudeEnvKey is the variable Claude Code reads for where it compacts,
// written into the "env" block of ~/.claude/settings.json.
//
// Not the top-level autoCompactWindow key: in 2.1.284 that key had no
// effect from a project's settings (context reached 124k against a 100k
// window without compacting), while the same value through "env"
// compacted at every crossing even from project settings. Settings "env"
// is the path verified to work at every level.
const claudeEnvKey = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"

// claudeSettingKey is the operator-facing setting; when they have set it,
// tokenops leaves Claude Code alone, since the env variable would
// override it.
const claudeSettingKey = "autoCompactWindow"

const (
	claudeMin = 100_000
	claudeMax = 1_000_000
)

func claudeEnvValue(m map[string]any) (int64, bool) {
	env, _ := m["env"].(map[string]any)
	s, ok := env[claudeEnvKey].(string)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v, err == nil
}

func applyClaude(p Paths, plan Plan, st *state, now time.Time) ([]Result, error) {
	if plan.ClaudeCompactAt <= 0 {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Dir(p.ClaudeSettings)); err != nil {
		return []Result{{Client: ClientClaudeCode, Status: StatusUnavailable, Note: "Claude Code is not set up here"}}, nil
	}
	m, err := readJSON(p.ClaudeSettings)
	if errors.Is(err, fs.ErrNotExist) {
		m, err = map[string]any{}, nil
	}
	if err != nil {
		return []Result{{Client: ClientClaudeCode, Path: p.ClaudeSettings, Status: StatusUnavailable, Note: err.Error()}}, err
	}
	want := min(max(plan.ClaudeCompactAt+ClaudeBuffer, claudeMin), claudeMax)
	r := Result{Client: ClientClaudeCode, Key: "env." + claudeEnvKey, Path: p.ClaudeSettings, Value: want}
	cur, has := claudeEnvValue(m)
	own, ownSet := intOf(m[claudeSettingKey])
	rec, ours := st.find(ClientClaudeCode, r.Key)
	switch {
	case ours && (!has || cur != rec.Value):
		st.drop(ClientClaudeCode, r.Key)
		r.Status, r.Value, r.Note = StatusEdited, cur, "changed since tokenops set it; left as you have it"
		return []Result{r}, nil
	case ours && cur == want:
		r.Status = StatusActive
		return []Result{r}, nil
	case !ours && has:
		r.Status, r.Value, r.Note = StatusYours, cur, "you set this yourself; left as it is"
		return []Result{r}, nil
	case !ours && ownSet:
		r.Key, r.Status, r.Value, r.Note = claudeSettingKey, StatusYours, own, "you set this yourself; left as it is"
		return []Result{r}, nil
	}
	ensure(m, "env")[claudeEnvKey] = strconv.FormatInt(want, 10)
	if err := st.editJSON(p.ClaudeSettings, m); err != nil {
		return nil, err
	}
	st.drop(ClientClaudeCode, r.Key)
	st.Applied = append(st.Applied, Setting{Client: ClientClaudeCode, Path: p.ClaudeSettings, Key: r.Key, Value: want, At: now.UTC()})
	r.Status = StatusApplied
	return []Result{r}, nil
}

func revertClaude(s Setting) (Result, error) {
	r := Result{Client: s.Client, Key: s.Key, Path: s.Path, Value: s.Value, Status: StatusReverted}
	m, err := readJSON(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if cur, ok := claudeEnvValue(m); !ok || cur != s.Value {
		r.Status, r.Note = StatusEdited, "changed since tokenops set it; left as you have it"
		return r, nil
	}
	env, _ := m["env"].(map[string]any)
	delete(env, claudeEnvKey)
	prune(m, "env")
	return r, writeJSON(s.Path, m)
}

// codexKey is Codex's top-level config key.
const codexKey = "model_auto_compact_token_limit"

// codexMarker labels the line tokenops writes, for a person reading the
// file.
const codexMarker = "# set by tokenops coach (context: autonomous)"

func applyCodex(p Paths, plan Plan, st *state, now time.Time) ([]Result, error) {
	if plan.CodexCompactAt <= 0 {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Dir(p.CodexConfig)); err != nil {
		return []Result{{Client: ClientCodex, Status: StatusUnavailable, Note: "Codex is not set up here"}}, nil
	}
	b, err := os.ReadFile(p.CodexConfig)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	src := string(b)
	r := Result{Client: ClientCodex, Key: codexKey, Path: p.CodexConfig, Value: plan.CodexCompactAt}
	cur, has := tomlTopLevelInt(src, codexKey)
	rec, ours := st.find(ClientCodex, codexKey)
	switch {
	case ours && (!has || cur != rec.Value):
		st.drop(ClientCodex, codexKey)
		r.Status, r.Value, r.Note = StatusEdited, cur, "changed since tokenops set it; left as you have it"
		return []Result{r}, nil
	case ours && cur == plan.CodexCompactAt:
		r.Status = StatusActive
		return []Result{r}, nil
	case !ours && has:
		r.Status, r.Value, r.Note = StatusYours, cur, "you set this yourself; left as it is"
		return []Result{r}, nil
	}
	mode := fs.FileMode(0o600)
	if fi, err := os.Stat(p.CodexConfig); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := st.editFile(p.CodexConfig, []byte(tomlSetTopLevel(src, codexKey, plan.CodexCompactAt)), mode); err != nil {
		return nil, err
	}
	st.drop(ClientCodex, codexKey)
	st.Applied = append(st.Applied, Setting{Client: ClientCodex, Path: p.CodexConfig, Key: codexKey, Value: plan.CodexCompactAt, Previous: rec.Previous, At: now.UTC()})
	r.Status = StatusApplied
	return []Result{r}, nil
}

func revertCodex(s Setting) (Result, error) {
	r := Result{Client: s.Client, Key: s.Key, Path: s.Path, Value: s.Value, Status: StatusReverted}
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	src := string(b)
	if cur, ok := tomlTopLevelInt(src, s.Key); !ok || cur != s.Value {
		r.Status, r.Note = StatusEdited, "changed since tokenops set it; left as you have it"
		return r, nil
	}
	out := tomlRemoveTopLevel(src, s.Key)
	if len(s.Previous) > 0 {
		var prev int64
		if json.Unmarshal(s.Previous, &prev) == nil {
			out = tomlSetTopLevel(out, s.Key, prev)
		}
	}
	fi, err := os.Stat(s.Path)
	if err != nil {
		return r, err
	}
	return r, writeAtomic(s.Path, []byte(out), fi.Mode().Perm())
}

var tomlKeyLine = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*=\s*(\d+)\b`)

// topLevelEnd is the index of the first table header line, where
// top-level keys end.
func topLevelEnd(lines []string) int {
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "[") {
			return i
		}
	}
	return len(lines)
}

func tomlTopLevelInt(src, key string) (int64, bool) {
	lines := strings.Split(src, "\n")
	for _, l := range lines[:topLevelEnd(lines)] {
		if m := tomlKeyLine.FindStringSubmatch(l); m != nil && m[1] == key {
			v, err := strconv.ParseInt(m[2], 10, 64)
			return v, err == nil
		}
	}
	return 0, false
}

// tomlSetTopLevel sets a top-level integer key, replacing its line when
// present and otherwise inserting it before the first table, where TOML
// requires top-level keys to be.
func tomlSetTopLevel(src, key string, v int64) string {
	line := fmt.Sprintf("%s = %d %s", key, v, codexMarker)
	lines := strings.Split(src, "\n")
	end := topLevelEnd(lines)
	for i, l := range lines[:end] {
		if m := tomlKeyLine.FindStringSubmatch(l); m != nil && m[1] == key {
			lines[i] = line
			return strings.Join(lines, "\n")
		}
	}
	if end == len(lines) {
		out := strings.TrimRight(src, "\n")
		if out != "" {
			out += "\n"
		}
		return out + line + "\n"
	}
	// No blank line is added around it, so removing the line restores the
	// file byte for byte.
	return strings.Join(append(lines[:end:end], append([]string{line}, lines[end:]...)...), "\n")
}

func tomlRemoveTopLevel(src, key string) string {
	lines := strings.Split(src, "\n")
	end := topLevelEnd(lines)
	for i, l := range lines[:end] {
		if m := tomlKeyLine.FindStringSubmatch(l); m != nil && m[1] == key {
			return strings.Join(append(lines[:i], lines[i+1:]...), "\n")
		}
	}
	return src
}

// opencodeLimits are a model's limits as models.dev gives them.
type opencodeLimits struct {
	Context int64 `json:"context"`
	Input   int64 `json:"input"`
	Output  int64 `json:"output"`
}

func loadOpencodeCatalog(path string) (map[string]opencodeLimits, error) {
	b, err := os.ReadFile(path) //nolint:gosec // opencode's own cache
	if err != nil {
		return nil, err
	}
	var raw map[string]struct {
		Models map[string]struct {
			Limit opencodeLimits `json:"limit"`
		} `json:"models"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := map[string]opencodeLimits{}
	for prov, p := range raw {
		for id, m := range p.Models {
			out[prov+"/"+id] = m.Limit
		}
	}
	return out, nil
}

// opencodeLimit returns the limit object for key ("provider/model") in an
// opencode config, if the config has one.
func opencodeLimit(cfg map[string]any, key string) (map[string]any, bool) {
	prov, model, ok := strings.Cut(key, "/")
	if !ok {
		return nil, false
	}
	m, ok := dig(cfg, "provider", prov, "models", model, "limit")
	return m, ok
}

func dig(m map[string]any, path ...string) (map[string]any, bool) {
	cur := m
	for _, k := range path {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// ensure walks path, creating objects that are missing.
func ensure(m map[string]any, path ...string) map[string]any {
	cur := m
	for _, k := range path {
		next, ok := cur[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
	return cur
}

func applyOpencode(p Paths, plan Plan, st *state, now time.Time) ([]Result, error) {
	if len(plan.OpencodeModels) == 0 || plan.OpencodeShare <= 0 {
		return nil, nil
	}
	catalog, err := loadOpencodeCatalog(p.OpencodeModels)
	if err != nil {
		return []Result{{Client: ClientOpencode, Status: StatusUnavailable, Note: "opencode's model catalog is not cached here, so its limits are unknown"}}, nil
	}
	cfg, err := readJSON(p.OpencodeConfig)
	if errors.Is(err, fs.ErrNotExist) {
		cfg, err = map[string]any{"$schema": "https://opencode.ai/config.json"}, nil
	}
	if err != nil {
		return []Result{{Client: ClientOpencode, Path: p.OpencodeConfig, Status: StatusUnavailable, Note: err.Error()}}, err
	}
	var out []Result
	changed := false
	for _, key := range plan.OpencodeModels {
		r, wrote := planOpencodeModel(p, plan, st, cfg, catalog, key, now)
		out = append(out, r)
		changed = changed || wrote
	}
	if changed {
		if err := st.editJSON(p.OpencodeConfig, cfg); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func planOpencodeModel(p Paths, plan Plan, st *state, cfg map[string]any, catalog map[string]opencodeLimits, key string, now time.Time) (Result, bool) {
	r := Result{Client: ClientOpencode, Key: key, Path: p.OpencodeConfig}
	lim, known := catalog[key]
	if !known || lim.Context <= 0 || lim.Output <= 0 {
		r.Status, r.Note = StatusUnavailable, "not in opencode's model catalog"
		return r, false
	}
	reserved := min(int64(opencodeReserved), lim.Output)
	want := int64(plan.OpencodeShare*float64(lim.Context)) + reserved
	r.Value = want
	current := lim.Input
	if current <= 0 {
		current = lim.Context
	}
	existing, has := opencodeLimit(cfg, key)
	curInput, _ := intOf(existing["input"])
	rec, ours := st.find(ClientOpencode, key)
	switch {
	case ours && (!has || curInput != rec.Value):
		st.drop(ClientOpencode, key)
		r.Status, r.Value, r.Note = StatusEdited, curInput, "changed since tokenops set it; left as you have it"
		return r, false
	case ours && curInput == want:
		r.Status = StatusActive
		return r, false
	case !ours && has:
		r.Status, r.Note = StatusYours, "you set this model's limits yourself; left as they are"
		return r, false
	case want >= current:
		r.Status, r.Note = StatusUnavailable, "already compacts at or before that point"
		return r, false
	}
	prov, model, _ := strings.Cut(key, "/")
	ensure(cfg, "provider", prov, "models", model)["limit"] = map[string]any{
		"context": lim.Context, "input": want, "output": lim.Output,
	}
	st.drop(ClientOpencode, key)
	st.Applied = append(st.Applied, Setting{Client: ClientOpencode, Path: p.OpencodeConfig, Key: key, Value: want, At: now.UTC()})
	r.Status = StatusApplied
	return r, true
}

func revertOpencode(s Setting) (Result, error) {
	r := Result{Client: s.Client, Key: s.Key, Path: s.Path, Value: s.Value, Status: StatusReverted}
	cfg, err := readJSON(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	lim, ok := opencodeLimit(cfg, s.Key)
	if cur, _ := intOf(lim["input"]); !ok || cur != s.Value {
		r.Status, r.Note = StatusEdited, "changed since tokenops set it; left as you have it"
		return r, nil
	}
	prov, model, _ := strings.Cut(s.Key, "/")
	m, _ := dig(cfg, "provider", prov, "models", model)
	delete(m, "limit")
	prune(cfg, "provider", prov, "models", model)
	return r, writeJSON(s.Path, cfg)
}

// prune removes the objects along path that tokenops' edit left empty,
// deepest first, so reverting leaves the file as it was.
func prune(m map[string]any, path ...string) {
	for depth := len(path); depth > 0; depth-- {
		parent, ok := dig(m, path[:depth-1]...)
		if !ok {
			return
		}
		child, ok := parent[path[depth-1]].(map[string]any)
		if !ok || len(child) > 0 {
			return
		}
		delete(parent, path[depth-1])
	}
}
