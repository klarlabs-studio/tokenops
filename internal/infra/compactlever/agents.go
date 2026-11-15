package compactlever

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"time"
)

// Gemini CLI and Pi each take a setting for where they compact, but in
// their own units, so both are sized from the model's context window as
// TokenOps knows it (Plan.Window). A model TokenOps cannot size is
// reported unavailable rather than set against a guessed window. Both are
// written only from the developer's own measured context line.
//
//   - Gemini CLI: model.compressionThreshold in ~/.gemini/settings.json,
//     the fraction of the model's window at which it compresses (default
//     0.5). It was model.chatCompression.contextPercentageThreshold, and
//     before that top-level chatCompression.contextPercentageThreshold; a
//     value under any of them is the operator's.
//   - Pi: compaction.modelOverrides["provider/modelId"].reserveTokens in
//     ~/.pi/agent/settings.json. Pi compacts once the context passes the
//     window less reserveTokens (default 16384).

// geminiKey is the dotted path of Gemini CLI's setting.
const geminiKey = "model.compressionThreshold"

// geminiThreshold is the fraction of window a line is, to two places, as
// Gemini CLI's own examples write it.
func geminiThreshold(line, window int64) float64 {
	return math.Round(float64(line)/float64(window)*100) / 100
}

// geminiOwn is the operator's own compression threshold, under the
// current key or either earlier one.
func geminiOwn(m map[string]any) bool {
	if model, ok := m["model"].(map[string]any); ok {
		if _, ok := model["compressionThreshold"]; ok {
			return true
		}
		if cc, ok := model["chatCompression"].(map[string]any); ok && cc["contextPercentageThreshold"] != nil {
			return true
		}
	}
	cc, ok := m["chatCompression"].(map[string]any)
	return ok && cc["contextPercentageThreshold"] != nil
}

func geminiValue(m map[string]any) (float64, bool) {
	model, _ := m["model"].(map[string]any)
	switch v := model["compressionThreshold"].(type) {
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	}
	return 0, false
}

func applyGemini(p Paths, plan Plan, st *state, now time.Time) ([]Result, error) {
	if plan.GeminiCompactAt <= 0 || p.GeminiSettings == "" {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Dir(p.GeminiSettings)); err != nil {
		return []Result{{Client: ClientGemini, Status: StatusUnavailable, Note: "Gemini CLI is not set up here"}}, nil
	}
	m, err := readJSON(p.GeminiSettings)
	if errors.Is(err, fs.ErrNotExist) {
		m, err = map[string]any{}, nil
	}
	if err != nil {
		return []Result{{Client: ClientGemini, Path: p.GeminiSettings, Status: StatusUnavailable, Note: err.Error()}}, err
	}
	r := Result{Client: ClientGemini, Key: geminiKey, Path: p.GeminiSettings, Value: plan.GeminiCompactAt}
	rec, ours := st.find(ClientGemini, geminiKey)
	cur, has := geminiValue(m)
	if ours {
		want := geminiThreshold(rec.Value, rec.Window)
		if !has || cur != want {
			st.drop(ClientGemini, geminiKey)
			r.Status, r.Note = StatusEdited, "changed since tokenops set it; left as you have it"
			return []Result{r}, nil
		}
	} else if geminiOwn(m) {
		r.Status, r.Note = StatusYours, "you set this yourself; left as it is"
		return []Result{r}, nil
	}
	model, _ := m["model"].(map[string]any)
	name, _ := model["name"].(string)
	window, known := int64(0), false
	if name != "" && plan.Window != nil {
		window, known = plan.Window(name)
	}
	switch {
	case name == "":
		r.Status, r.Note = StatusUnavailable, "Gemini CLI's settings name no model, so its context window is unknown here"
		return []Result{r}, nil
	case !known:
		r.Status, r.Note = StatusUnavailable, "TokenOps does not know "+name+"'s context window"
		return []Result{r}, nil
	case plan.GeminiCompactAt >= window:
		r.Status, r.Note = StatusUnavailable, "already compacts before that point"
		return []Result{r}, nil
	}
	want := geminiThreshold(plan.GeminiCompactAt, window)
	if ours && rec.Value == plan.GeminiCompactAt && rec.Window == window {
		r.Status = StatusActive
		return []Result{r}, nil
	}
	ensure(m, "model")["compressionThreshold"] = want
	if err := st.editJSON(p.GeminiSettings, m); err != nil {
		return nil, err
	}
	st.drop(ClientGemini, geminiKey)
	st.Applied = append(st.Applied, Setting{Client: ClientGemini, Path: p.GeminiSettings, Key: geminiKey,
		Value: plan.GeminiCompactAt, Window: window, At: now.UTC()})
	r.Status = StatusApplied
	return []Result{r}, nil
}

func revertGemini(s Setting) (Result, error) {
	r := Result{Client: s.Client, Key: s.Key, Path: s.Path, Value: s.Value, Status: StatusReverted}
	m, err := readJSON(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if cur, ok := geminiValue(m); !ok || cur != geminiThreshold(s.Value, s.Window) {
		r.Status, r.Note = StatusEdited, "changed since tokenops set it; left as you have it"
		return r, nil
	}
	model, _ := m["model"].(map[string]any)
	delete(model, "compressionThreshold")
	prune(m, "model")
	return r, writeJSON(s.Path, m)
}

// piOverride is the model's override object in Pi's settings, if any.
func piOverride(m map[string]any, key string) (map[string]any, bool) {
	ov, ok := dig(m, "compaction", "modelOverrides")
	if !ok {
		return nil, false
	}
	o, ok := ov[key].(map[string]any)
	return o, ok
}

// piOwn says why Pi's compaction is the operator's for every model: auto-
// compaction switched off, or a reserve they set for all models.
func piOwn(m map[string]any) string {
	c, _ := m["compaction"].(map[string]any)
	if on, ok := c["enabled"].(bool); ok && !on {
		return "you switched Pi's auto-compaction off; left as it is"
	}
	if _, ok := c["reserveTokens"]; ok {
		return "you set Pi's reserve yourself; left as it is"
	}
	return ""
}

func applyPi(p Paths, plan Plan, st *state, now time.Time) ([]Result, error) {
	if plan.PiCompactAt <= 0 || len(plan.PiModels) == 0 || p.PiSettings == "" {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Dir(p.PiSettings)); err != nil {
		return []Result{{Client: ClientPi, Status: StatusUnavailable, Note: "Pi is not set up here"}}, nil
	}
	m, err := readJSON(p.PiSettings)
	if errors.Is(err, fs.ErrNotExist) {
		m, err = map[string]any{}, nil
	}
	if err != nil {
		return []Result{{Client: ClientPi, Path: p.PiSettings, Status: StatusUnavailable, Note: err.Error()}}, err
	}
	own := piOwn(m)
	var out []Result
	changed := false
	for _, key := range plan.PiModels {
		r, wrote := planPiModel(p, plan, st, m, key, own, now)
		out = append(out, r)
		changed = changed || wrote
	}
	if changed {
		if err := st.editJSON(p.PiSettings, m); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func planPiModel(p Paths, plan Plan, st *state, m map[string]any, key, own string, now time.Time) (Result, bool) {
	r := Result{Client: ClientPi, Key: key, Path: p.PiSettings}
	o, has := piOverride(m, key)
	cur, hasReserve := intOf(o["reserveTokens"])
	hasReserve = has && hasReserve
	rec, ours := st.find(ClientPi, key)
	switch {
	case ours && (!hasReserve || cur != rec.Value):
		st.drop(ClientPi, key)
		r.Status, r.Value, r.Note = StatusEdited, cur, "changed since tokenops set it; left as you have it"
		return r, false
	case !ours && own != "":
		r.Status, r.Note = StatusYours, own
		return r, false
	case !ours && hasReserve:
		r.Status, r.Value, r.Note = StatusYours, cur, "you set this model's reserve yourself; left as it is"
		return r, false
	}
	_, model := splitPiKey(key)
	window, known := int64(0), false
	if plan.Window != nil {
		window, known = plan.Window(model)
	}
	if !known {
		r.Status, r.Note = StatusUnavailable, "TokenOps does not know this model's context window"
		return r, false
	}
	if plan.PiCompactAt >= window {
		r.Status, r.Note = StatusUnavailable, "already compacts before that point"
		return r, false
	}
	want := window - plan.PiCompactAt
	r.Value = want
	if ours && cur == want {
		r.Status = StatusActive
		return r, false
	}
	ov := ensure(m, "compaction", "modelOverrides")
	entry, _ := ov[key].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
		ov[key] = entry
	}
	entry["reserveTokens"] = want
	st.drop(ClientPi, key)
	st.Applied = append(st.Applied, Setting{Client: ClientPi, Path: p.PiSettings, Key: key, Value: want, Window: window, At: now.UTC()})
	r.Status = StatusApplied
	return r, true
}

// splitPiKey splits "provider/modelId" at the first slash: a model ID may
// carry slashes of its own.
func splitPiKey(key string) (provider, model string) {
	for i := range len(key) {
		if key[i] == '/' {
			return key[:i], key[i+1:]
		}
	}
	return "", key
}

func revertPi(s Setting) (Result, error) {
	r := Result{Client: s.Client, Key: s.Key, Path: s.Path, Value: s.Value, Status: StatusReverted}
	m, err := readJSON(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	o, ok := piOverride(m, s.Key)
	if cur, _ := intOf(o["reserveTokens"]); !ok || cur != s.Value {
		r.Status, r.Note = StatusEdited, "changed since tokenops set it; left as you have it"
		return r, nil
	}
	delete(o, "reserveTokens")
	ov, _ := dig(m, "compaction", "modelOverrides")
	if len(o) == 0 {
		delete(ov, s.Key)
	}
	prune(m, "compaction", "modelOverrides")
	return r, writeJSON(s.Path, m)
}

// piValue is the reserve Pi's settings hold for the model key.
func piValue(m map[string]any, key string) (int64, bool) {
	o, ok := piOverride(m, key)
	if !ok {
		return 0, false
	}
	return intOf(o["reserveTokens"])
}
