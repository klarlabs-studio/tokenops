// Package compactlever sets where coding agents compact their context on
// their own (coach power "context", autonomy autonomous, ADR 0006).
//
// No agent lets a hook compact a session, but three let a setting move the
// point at which they compact themselves, each verified in a real run
// (2026-09-30):
//
//   - Claude Code: CLAUDE_CODE_AUTO_COMPACT_WINDOW, through the "env"
//     block of ~/.claude/settings.json. The session compacts about 31k
//     below it (68.9k with it at 100k), as the default compacts at ~967k
//     of a 1M window. The top-level autoCompactWindow key did nothing from
//     project settings, so tokenops does not rely on it.
//   - Codex: top-level `model_auto_compact_token_limit` in
//     ~/.codex/config.toml. The session compacts before reaching it.
//   - opencode: `provider.<id>.models.<id>.limit` in opencode.json, which
//     must carry context and output as well; the session compacts once
//     input reaches `input` less the reserved buffer (20k by default).
//     Setting `context` alone changes nothing when the model defines an
//     input limit.
//
// Cursor summarises on its own terms and has no such setting.
//
// Every change is recorded with what it replaced, so reverting restores
// exactly that, and a value the operator set or changed is never touched.
package compactlever

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Clients the package can set.
const (
	ClientClaudeCode = "claude-code"
	ClientCodex      = "codex"
	ClientOpencode   = "opencode"
	ClientCursor     = "cursor"
)

// Status is what became of one setting.
type Status string

// The statuses.
const (
	// StatusApplied is a setting tokenops wrote.
	StatusApplied Status = "applied"
	// StatusActive is a setting tokenops wrote earlier that is still in
	// place.
	StatusActive Status = "active"
	// StatusReverted is a setting tokenops restored to what it replaced.
	StatusReverted Status = "reverted"
	// StatusYours is a value the operator set, which tokenops leaves.
	StatusYours Status = "yours"
	// StatusEdited is a value tokenops wrote that the operator has since
	// changed; it is theirs now.
	StatusEdited Status = "edited"
	// StatusUnavailable is a client without such a setting, or one not
	// installed here.
	StatusUnavailable Status = "unavailable"
)

// Result is one client's setting after an operation.
type Result struct {
	Client string `json:"client"`
	// Key names the setting and, for opencode, the model it applies to.
	Key    string `json:"key,omitempty"`
	Path   string `json:"path,omitempty"`
	Value  int64  `json:"value,omitempty"`
	Status Status `json:"status"`
	Note   string `json:"note,omitempty"`
}

// Setting is one value tokenops wrote, with what it replaced.
type Setting struct {
	Client string `json:"client"`
	Path   string `json:"path"`
	Key    string `json:"key"`
	Value  int64  `json:"value"`
	// Previous is the raw JSON of what the key held before, or empty when
	// it was absent. For opencode it is the whole limit object.
	Previous json.RawMessage `json:"previous,omitempty"`
	At       time.Time       `json:"at"`
}

// Paths locates each client's files. Empty fields take the defaults under
// the operator's home directory.
type Paths struct {
	ClaudeSettings string
	CodexConfig    string
	OpencodeConfig string
	// OpencodeModels is opencode's cache of models.dev, where each model's
	// true context and output limits come from.
	OpencodeModels string
	// State records what tokenops wrote.
	State string
}

// DefaultPaths returns the standard locations.
func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	// opencode follows the XDG directories; Claude Code and Codex keep
	// their files under the home directory.
	configHome := envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	cacheHome := envOr("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	return Paths{
		ClaudeSettings: filepath.Join(home, ".claude", "settings.json"),
		CodexConfig:    filepath.Join(envOr("CODEX_HOME", filepath.Join(home, ".codex")), "config.toml"),
		OpencodeConfig: filepath.Join(configHome, "opencode", "opencode.json"),
		OpencodeModels: filepath.Join(cacheHome, "opencode", "models.json"),
		State:          filepath.Join(home, ".tokenops", "coach", "compaction.json"),
	}, nil
}

// Plan is what to set.
type Plan struct {
	// ClaudeCompactAt is where Claude Code should compact.
	ClaudeCompactAt int64
	// CodexCompactAt is where Codex should compact.
	CodexCompactAt int64
	// OpencodeModels are the provider/model pairs opencode runs on here,
	// such as "anthropic/claude-opus-4-8"; each is set to compact at
	// OpencodeShare of its context window.
	OpencodeModels []string
	OpencodeShare  float64
}

// ClaudeBuffer is how far below autoCompactWindow Claude Code compacts:
// ~967k of a 1M window by default, 68.9k of a 100k window when measured.
const ClaudeBuffer = 33_000

// opencodeReserved is opencode's default buffer below the input limit.
const opencodeReserved = 20_000

type state struct {
	Applied []Setting `json:"applied"`
	// Files holds each edited file as it was before tokenops' first edit,
	// and a digest of it as tokenops last wrote it. When nothing else has
	// touched the file since, reverting puts the original bytes back, so
	// the operator's formatting and key order survive.
	Files map[string]fileSnapshot `json:"files,omitempty"`
}

type fileSnapshot struct {
	Original []byte `json:"original"`
	Existed  bool   `json:"existed"`
	Written  string `json:"written"`
}

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// editFile writes b to path, keeping the file's pre-edit bytes the first
// time tokenops touches it.
func (st *state) editFile(path string, b []byte, mode fs.FileMode) error {
	if st.Files == nil {
		st.Files = map[string]fileSnapshot{}
	}
	snap, seen := st.Files[path]
	if !seen {
		orig, err := os.ReadFile(path) //nolint:gosec // the operator's own config
		snap = fileSnapshot{Original: orig, Existed: err == nil}
	}
	if err := writeAtomic(path, b, mode); err != nil {
		return err
	}
	snap.Written = digest(b)
	st.Files[path] = snap
	return nil
}

// restoreUntouched puts back the original bytes of every file that still
// holds exactly what tokenops last wrote, and reports the settings those
// files carried as reverted.
func (st *state) restoreUntouched() ([]Result, error) {
	var out []Result
	var errs []error
	for path, snap := range st.Files {
		cur, err := os.ReadFile(path) //nolint:gosec // the operator's own config
		if err != nil || digest(cur) != snap.Written {
			continue
		}
		if snap.Existed {
			fi, statErr := os.Stat(path)
			if statErr != nil {
				errs = append(errs, statErr)
				continue
			}
			err = writeAtomic(path, snap.Original, fi.Mode().Perm())
		} else {
			err = os.Remove(path)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, s := range slices.Clone(st.Applied) {
			if s.Path == path {
				out = append(out, Result{Client: s.Client, Key: s.Key, Path: s.Path, Value: s.Value, Status: StatusReverted})
				st.drop(s.Client, s.Key)
			}
		}
		delete(st.Files, path)
	}
	return out, errors.Join(errs...)
}

func loadState(path string) state {
	var st state
	if b, err := os.ReadFile(path); err == nil { //nolint:gosec // tokenops' own state
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func saveState(path string, st state) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, b, 0o600)
}

func (st state) find(client, key string) (Setting, bool) {
	for _, s := range st.Applied {
		if s.Client == client && s.Key == key {
			return s, true
		}
	}
	return Setting{}, false
}

func (st *state) drop(client, key string) {
	st.Applied = slices.DeleteFunc(st.Applied, func(s Setting) bool { return s.Client == client && s.Key == key })
}

// Apply writes the plan into every client that has a setting for it.
func Apply(p Paths, plan Plan, now time.Time) ([]Result, error) {
	st := loadState(p.State)
	var out []Result
	var errs []error
	for _, apply := range []func(Paths, Plan, *state, time.Time) ([]Result, error){applyClaude, applyCodex, applyOpencode} {
		rs, err := apply(p, plan, &st, now)
		out = append(out, rs...)
		errs = append(errs, err)
	}
	out = append(out, Result{Client: ClientCursor, Status: StatusUnavailable, Note: "Cursor summarises on its own and has no setting for when"})
	if err := saveState(p.State, st); err != nil {
		errs = append(errs, err)
	}
	return out, errors.Join(errs...)
}

// Revert restores what every recorded setting replaced, unless the
// operator has changed it since.
func Revert(p Paths) ([]Result, error) {
	st := loadState(p.State)
	out, err := st.restoreUntouched()
	errs := []error{err}
	for _, s := range slices.Clone(st.Applied) {
		var r Result
		var err error
		switch s.Client {
		case ClientClaudeCode:
			r, err = revertClaude(s)
		case ClientCodex:
			r, err = revertCodex(s)
		case ClientOpencode:
			r, err = revertOpencode(s)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		st.drop(s.Client, s.Key)
		out = append(out, r)
	}
	// Files reverted key by key were touched by something else too; their
	// snapshots no longer describe them.
	for path := range st.Files {
		if !slices.ContainsFunc(st.Applied, func(s Setting) bool { return s.Path == path }) {
			delete(st.Files, path)
		}
	}
	if err := saveState(p.State, st); err != nil {
		errs = append(errs, err)
	}
	return out, errors.Join(errs...)
}

// Check reports each recorded setting as it stands, without changing
// anything.
func Check(p Paths) []Result {
	st := loadState(p.State)
	out := make([]Result, 0, len(st.Applied))
	for _, s := range st.Applied {
		r := Result{Client: s.Client, Key: s.Key, Path: s.Path, Value: s.Value, Status: StatusActive}
		cur, ok := currentValue(s)
		if !ok || cur != s.Value {
			r.Status = StatusEdited
			r.Note = "changed since tokenops set it"
		}
		out = append(out, r)
	}
	return out
}

func currentValue(s Setting) (int64, bool) {
	switch s.Client {
	case ClientClaudeCode:
		m, err := readJSON(s.Path)
		if err != nil {
			return 0, false
		}
		return claudeEnvValue(m)
	case ClientCodex:
		b, err := os.ReadFile(s.Path) //nolint:gosec // the operator's own config
		if err != nil {
			return 0, false
		}
		return tomlTopLevelInt(string(b), s.Key)
	case ClientOpencode:
		m, err := readJSON(s.Path)
		if err != nil {
			return 0, false
		}
		limit, ok := opencodeLimit(m, s.Key)
		if !ok {
			return 0, false
		}
		return intOf(limit["input"])
	}
	return 0, false
}

func intOf(v any) (int64, bool) {
	f, ok := v.(float64)
	return int64(f), ok
}

func readJSON(path string) (map[string]any, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator's own config
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("%s is not plain JSON (%w); leaving it alone", path, err)
		}
	}
	return m, nil
}

func writeJSON(path string, m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	mode := fs.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	return writeAtomic(path, append(b, '\n'), mode)
}

// writeAtomic replaces path in one rename so an agent reading its
// settings never sees half a file.
func writeAtomic(path string, b []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tokenops-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// editJSON is editFile for a JSON document, in the indentation the
// agents' own files use.
func (st *state) editJSON(path string, m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	mode := fs.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	return st.editFile(path, append(b, '\n'), mode)
}
