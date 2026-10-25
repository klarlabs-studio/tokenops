// Package jsonfile edits JSON configuration files that belong to the
// operator (Claude Code's settings.json, opencode.json) without
// reformatting what it did not change.
//
// Callers decode a file into a map, change the keys they own, and hand the
// map back. Render compares the map with the file's own text: every value
// that is semantically unchanged keeps its original bytes, objects keep their
// key order, new keys are appended, and keys the map no longer holds are
// removed. New values are encoded without HTML escaping, so a hook command
// such as `a && b 2>/dev/null` stays readable.
package jsonfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// DefaultMode is the permission of a file that did not exist before.
const DefaultMode fs.FileMode = 0o600

const defaultIndent = "  "

// Parse decodes a JSON object. Numbers are kept as json.Number so integers
// beyond 2^53 survive a round trip. Empty input is an empty object.
func Parse(b []byte) (map[string]any, error) {
	m := map[string]any{}
	if len(bytes.TrimSpace(b)) == 0 {
		return m, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("trailing data after the JSON object")
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// Read loads path as a JSON object. A missing file returns an error that
// wraps fs.ErrNotExist; a file that is not plain JSON is refused.
func Read(path string) (map[string]any, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator's own config
	if err != nil {
		return nil, err
	}
	m, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s is not plain JSON (%w); leaving it alone", path, err)
	}
	return m, nil
}

// Write stores m at path, keeping the file's formatting for everything m did
// not change, the file's mode, and replacing the file atomically. A file
// that exists but is not plain JSON is refused rather than overwritten.
func Write(path string, m map[string]any) error {
	b, err := Edit(path, m)
	if err != nil {
		return err
	}
	return WriteAtomic(path, b, ModeOf(path))
}

// Edit is Render for the file at path; a missing file renders from nothing.
func Edit(path string, m map[string]any) ([]byte, error) {
	old, err := os.ReadFile(path) //nolint:gosec // the operator's own config
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	out, err := Render(old, m)
	if err != nil {
		return nil, fmt.Errorf("%s is not plain JSON (%w); leaving it alone", path, err)
	}
	return out, nil
}

// ModeOf is the permission of path, DefaultMode when it does not exist.
func ModeOf(path string) fs.FileMode {
	if fi, err := os.Stat(path); err == nil {
		return fi.Mode().Perm()
	}
	return DefaultMode
}

// Render returns the document old would become if it held m: unchanged
// values keep their bytes and key order, changed values are re-encoded in
// place, new keys follow the last old one, removed keys are dropped. The
// result ends with a newline. old must be empty or a JSON object.
func Render(old []byte, m map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	if len(bytes.TrimSpace(old)) == 0 {
		if err := writeObject(&buf, nil, m, "", defaultIndent); err != nil {
			return nil, err
		}
		buf.WriteByte('\n')
		return buf.Bytes(), nil
	}
	trimmed := bytes.TrimSpace(old)
	if _, err := Parse(trimmed); err != nil {
		return nil, err
	}
	fields, err := splitObject(trimmed)
	if err != nil {
		return nil, err
	}
	if err := writeObject(&buf, fields, m, "", indentOf(old)); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

type field struct {
	key string
	raw json.RawMessage
}

// splitObject lists an object's members in file order.
func splitObject(b []byte) ([]field, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	var out []field
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		out = append(out, field{key: kt.(string), raw: raw})
	}
	return out, nil
}

// writeObject writes the object m, using old for the order and bytes of what
// it still holds. depth is the indent of the line the object opens on.
func writeObject(buf *bytes.Buffer, old []field, m map[string]any, depth, unit string) error {
	keys := orderedKeys(old, m)
	if len(keys) == 0 {
		buf.WriteString("{}")
		return nil
	}
	oldBy := make(map[string]json.RawMessage, len(old))
	for _, f := range old {
		oldBy[f.key] = f.raw
	}
	inner := depth + unit
	buf.WriteString("{\n")
	for i, k := range keys {
		kb, err := encode(k, "", unit)
		if err != nil {
			return err
		}
		buf.WriteString(inner)
		buf.Write(kb)
		buf.WriteString(": ")
		if err := writeValue(buf, oldBy[k], m[k], inner, unit); err != nil {
			return err
		}
		if i < len(keys)-1 {
			buf.WriteByte(',')
		}
		buf.WriteByte('\n')
	}
	buf.WriteString(depth + "}")
	return nil
}

// orderedKeys is old's keys that m still has, in file order, then m's new
// keys sorted.
func orderedKeys(old []field, m map[string]any) []string {
	seen := make(map[string]bool, len(m))
	keys := make([]string, 0, len(m))
	for _, f := range old {
		if _, ok := m[f.key]; ok && !seen[f.key] {
			seen[f.key] = true
			keys = append(keys, f.key)
		}
	}
	var added []string
	for k := range m {
		if !seen[k] {
			added = append(added, k)
		}
	}
	sortStrings(added)
	return append(keys, added...)
}

// writeValue writes one value: the old bytes when it is unchanged, a merge
// when both sides are objects, a fresh encoding otherwise.
func writeValue(buf *bytes.Buffer, raw json.RawMessage, v any, depth, unit string) error {
	if raw != nil {
		var cur any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if dec.Decode(&cur) == nil {
			if reflect.DeepEqual(cur, v) {
				buf.Write(raw)
				return nil
			}
			if nm, ok := v.(map[string]any); ok {
				if _, isObj := cur.(map[string]any); isObj {
					fields, err := splitObject(raw)
					if err != nil {
						return err
					}
					return writeObject(buf, fields, nm, depth, unit)
				}
			}
		}
	}
	b, err := encode(v, depth, unit)
	if err != nil {
		return err
	}
	buf.Write(b)
	return nil
}

// encode renders v without HTML escaping, its continuation lines indented
// from depth.
func encode(v any, depth, unit string) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent(depth, unit)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// indentOf is the file's own indent unit: the leading whitespace of its
// first indented line.
func indentOf(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed != "" && len(trimmed) < len(line) {
			return line[:len(line)-len(trimmed)]
		}
	}
	return defaultIndent
}

// WriteAtomic replaces path in one rename, so a reader never sees half a
// file.
func WriteAtomic(path string, b []byte, mode fs.FileMode) error {
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

func sortStrings(s []string) { sort.Strings(s) }
