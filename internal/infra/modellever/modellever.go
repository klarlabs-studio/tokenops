// Package modellever sets the model new Claude Code sessions start on
// (coach power "models", autonomy autonomous, while the plan is pressed).
//
// A hook cannot change a running session's model, but Claude Code reads
// the top-level "model" key of ~/.claude/settings.json when a session
// starts (https://code.claude.com/docs/en/model-config: it ranks below
// /model, --model and ANTHROPIC_MODEL, and /model itself saves its choice
// there). Setting that key is the one lever on the main session's model.
//
// Ownership follows the context power's lever (compactlever): a model the
// operator set is theirs and never touched; a model tokenops set is
// recorded with when and why, kept only while it still holds exactly that
// value, and removed again when the reason passes. A /model choice after
// tokenops set the key replaces the value and so makes it the operator's.
package modellever

import (
	"encoding/json"
	"errors"
	"go.klarlabs.de/tokenops/internal/infra/claudehome"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/jsonfile"
)

// Status is what became of the setting.
type Status string

// The statuses, as the context power's lever names them.
const (
	StatusApplied     Status = "applied"
	StatusActive      Status = "active"
	StatusReverted    Status = "reverted"
	StatusYours       Status = "yours"
	StatusEdited      Status = "edited"
	StatusUnavailable Status = "unavailable"
	// StatusNone is no setting of tokenops' and nothing to do.
	StatusNone Status = "none"
)

// settingKey is Claude Code's settings key for the default model.
const settingKey = "model"

// Paths locates the files. Empty fields take the defaults.
type Paths struct {
	ClaudeSettings string
	// State records what tokenops wrote.
	State string
}

// DefaultPaths returns ~/.claude/settings.json and tokenops' record of it.
func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		ClaudeSettings: claudehome.Join("settings.json"),
		State:          filepath.Join(home, ".tokenops", "coach", "default-model.json"),
	}, nil
}

// Setting is the model tokenops set, with why.
type Setting struct {
	Path string `json:"path"`
	// Value is the model written; From the model sessions ran on before.
	Value string `json:"value"`
	From  string `json:"from,omitempty"`
	// Reason is the move in the operator's words, Evidence the figures it
	// rested on, MoveID its entry in the follow-through ledger.
	Reason   string `json:"reason,omitempty"`
	Evidence string `json:"evidence,omitempty"`
	MoveID   string `json:"move_id,omitempty"`
	// Until is when the pressed window resets, when known: past it the
	// reason has passed even without a fresh reading.
	Until time.Time `json:"until,omitzero"`
	At    time.Time `json:"at"`
}

// Result is the setting after an operation.
type Result struct {
	Status Status `json:"status"`
	// Model is the value in the settings file now (or, reverted, the one
	// removed).
	Model   string   `json:"model,omitempty"`
	Path    string   `json:"path,omitempty"`
	Note    string   `json:"note,omitempty"`
	Setting *Setting `json:"setting,omitempty"`
}

type state struct {
	Applied *Setting `json:"applied,omitempty"`
	// Advised names the pressed window the coach last advised about,
	// so the advice is given once per window rather than every turn.
	Advised string `json:"advised,omitempty"`
}

// Advise reports whether advice about window is still to be given, and
// marks it given. A state that cannot be saved advises nothing: repeating
// the advice every turn is worse than missing it once.
func Advise(p Paths, window string) bool {
	st := loadState(p.State)
	if window == "" || st.Advised == window {
		return false
	}
	st.Advised = window
	return saveState(p.State, st) == nil
}

// Current is tokenops' setting as it stands, nil when there is none.
// A recorded setting the file no longer holds is reported edited.
func Current(p Paths) (*Setting, Result) {
	st := loadState(p.State)
	if st.Applied == nil {
		return nil, Result{Status: StatusNone}
	}
	m, err := readJSON(p.ClaudeSettings)
	cur, _ := m[settingKey].(string)
	if err != nil || cur != st.Applied.Value {
		return st.Applied, Result{Status: StatusEdited, Model: cur, Path: p.ClaudeSettings, Note: "changed since tokenops set it"}
	}
	return st.Applied, Result{Status: StatusActive, Model: cur, Path: p.ClaudeSettings, Setting: st.Applied}
}

// Apply sets the default model to want.Value unless the operator set one
// themselves. It is idempotent: a setting already in place is left, and a
// value changed since tokenops wrote it becomes the operator's.
func Apply(p Paths, want Setting, now time.Time) (Result, error) {
	if want.Value == "" {
		return Result{Status: StatusNone}, nil
	}
	if _, err := os.Stat(filepath.Dir(p.ClaudeSettings)); err != nil {
		return Result{Status: StatusUnavailable, Note: "Claude Code is not set up here"}, nil
	}
	m, err := readJSON(p.ClaudeSettings)
	if errors.Is(err, fs.ErrNotExist) {
		m, err = map[string]any{}, nil
	}
	if err != nil {
		return Result{Status: StatusUnavailable, Path: p.ClaudeSettings, Note: err.Error()}, err
	}
	st := loadState(p.State)
	cur, _ := m[settingKey].(string)
	_, set := m[settingKey]
	r := Result{Path: p.ClaudeSettings, Model: cur}
	switch {
	case st.Applied != nil && cur != st.Applied.Value:
		st.Applied = nil
		r.Status, r.Note = StatusEdited, "changed since tokenops set it; left as you have it"
		return r, saveState(p.State, st)
	case st.Applied != nil && cur == want.Value:
		r.Status, r.Setting = StatusActive, st.Applied
		return r, nil
	case st.Applied == nil && set:
		r.Status, r.Note = StatusYours, "you set this yourself; left as it is"
		return r, nil
	}
	m[settingKey] = want.Value
	if err := writeJSON(p.ClaudeSettings, m); err != nil {
		return Result{}, err
	}
	want.Path, want.At = p.ClaudeSettings, now.UTC()
	st.Applied = &want
	if err := saveState(p.State, st); err != nil {
		return Result{}, err
	}
	return Result{Status: StatusApplied, Model: want.Value, Path: p.ClaudeSettings, Setting: &want}, nil
}

// Revert removes the model tokenops set, unless the operator has changed
// it since; then it is theirs and stays.
func Revert(p Paths) (Result, error) {
	st := loadState(p.State)
	if st.Applied == nil {
		return Result{Status: StatusNone}, nil
	}
	rec := st.Applied
	st.Applied = nil
	r := Result{Status: StatusReverted, Model: rec.Value, Path: rec.Path, Setting: rec}
	m, err := readJSON(rec.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return r, saveState(p.State, st)
	case err != nil:
		st.Applied = rec // try again next time
		return Result{}, err
	}
	if cur, _ := m[settingKey].(string); cur != rec.Value {
		r.Status, r.Model, r.Note = StatusEdited, cur, "changed since tokenops set it; left as you have it"
		return r, saveState(p.State, st)
	}
	delete(m, settingKey)
	if err := writeJSON(rec.Path, m); err != nil {
		st.Applied = rec
		return Result{}, err
	}
	return r, saveState(p.State, st)
}

func loadState(path string) state {
	var st state
	if b, err := os.ReadFile(path); err == nil { //nolint:gosec // tokenops' own state
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func saveState(path string, st state) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, b, 0o600)
}

func readJSON(path string) (map[string]any, error) { return jsonfile.Read(path) }

// writeJSON stores m as the operator's file, keeping its key order and
// formatting for everything m did not change.
func writeJSON(path string, m map[string]any) error { return jsonfile.Write(path, m) }

// writeAtomic replaces path in one rename, so Claude Code reading its
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
