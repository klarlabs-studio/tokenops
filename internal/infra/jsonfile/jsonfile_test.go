package jsonfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixture = `{
  "zeta": true,
  "model": "opus",
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {"type": "command", "command": "a && b 2>/dev/null; echo '<done> é 日本'"}
        ]
      }
    ]
  },
  "env": {
    "ZED": "1",
    "BIG": 9007199254740993,
    "CTX": 633000
  },
  "alpha": [1, 2,   3]
}
`

func write(t *testing.T, content string, mode fs.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func lines(s string) map[string]bool {
	m := map[string]bool{}
	for _, l := range strings.Split(s, "\n") {
		m[l] = true
	}
	return m
}

func TestWriteUnchangedKeepsFileExceptNothing(t *testing.T) {
	p := write(t, fixture, 0o640)
	m, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(p, m); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != fixture {
		t.Fatalf("unchanged round trip altered the file:\n%s", got)
	}
}

func TestSetTopLevelKeyChangesOnlyThatKey(t *testing.T) {
	p := write(t, fixture, 0o640)
	m, _ := Read(p)
	m["model"] = "sonnet <fast> & cheap"
	if err := Write(p, m); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	want := strings.Replace(fixture, `"model": "opus"`, `"model": "sonnet <fast> & cheap"`, 1)
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(string(got), "\\u00"+"26") || strings.Contains(string(got), "\\u00"+"3c") {
		t.Fatal("HTML escaped")
	}
}

func TestNestedChangeKeepsOrderAndSiblingBytes(t *testing.T) {
	p := write(t, fixture, 0o600)
	m, _ := Read(p)
	m["env"].(map[string]any)["CTX"] = "700000"
	m["env"].(map[string]any)["NEW"] = "x"
	if err := Write(p, m); err != nil {
		t.Fatal(err)
	}
	got := string(mustRead(t, p))
	for _, keep := range []string{`"ZED": "1"`, `"BIG": 9007199254740993`, `a && b 2>/dev/null; echo '<done> é 日本'`, `"alpha": [1, 2,   3]`} {
		if !strings.Contains(got, keep) {
			t.Fatalf("lost %q in:\n%s", keep, got)
		}
	}
	if !inOrder(got, `"ZED"`, `"BIG"`, `"CTX"`, `"NEW"`) {
		t.Fatalf("env order lost:\n%s", got)
	}
	if !strings.Contains(got, `"CTX": "700000"`) {
		t.Fatalf("CTX not set:\n%s", got)
	}
}

func TestDeleteKeyRemovesOnlyItsLines(t *testing.T) {
	p := write(t, fixture, 0o600)
	m, _ := Read(p)
	delete(m, "model")
	if err := Write(p, m); err != nil {
		t.Fatal(err)
	}
	got := string(mustRead(t, p))
	want := strings.Replace(fixture, "  \"model\": \"opus\",\n", "", 1)
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestNewKeyAppendedAtEnd(t *testing.T) {
	p := write(t, fixture, 0o600)
	m, _ := Read(p)
	m["added"] = map[string]any{"b": 1, "a": "x&y"}
	if err := Write(p, m); err != nil {
		t.Fatal(err)
	}
	got := string(mustRead(t, p))
	tail := "  \"alpha\": [1, 2,   3],\n  \"added\": {\n    \"a\": \"x&y\",\n    \"b\": 1\n  }\n}\n"
	if !strings.HasSuffix(got, tail) {
		t.Fatalf("bad tail:\n%s", got)
	}
	orig := lines(fixture)
	for _, l := range strings.Split(got, "\n") {
		if !orig[l] && !strings.Contains(tail, l) {
			t.Fatalf("unexpected line %q", l)
		}
	}
}

func TestModeKeptAndNewFileDefault(t *testing.T) {
	p := write(t, fixture, 0o640)
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	m, _ := Read(p)
	m["x"] = true
	if err := Write(p, m); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	np := filepath.Join(t.TempDir(), "sub", "new.json")
	if err := Write(np, map[string]any{"b": "<&>", "a": 1}); err != nil {
		t.Fatal(err)
	}
	fi, _ = os.Stat(np)
	if fi.Mode().Perm() != DefaultMode {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	if got := string(mustRead(t, np)); got != "{\n  \"a\": 1,\n  \"b\": \"<&>\"\n}\n" {
		t.Fatalf("got %q", got)
	}
}

func TestInvalidJSONRefusedAndUntouched(t *testing.T) {
	for _, bad := range []string{`{"a": `, `[1,2]`, `{"a":1} trailing`, `// c` + "\n{}"} {
		p := write(t, bad, 0o600)
		if _, err := Read(p); err == nil || !strings.Contains(err.Error(), "not plain JSON") {
			t.Fatalf("Read(%q): %v", bad, err)
		}
		if err := Write(p, map[string]any{"a": 1}); err == nil {
			t.Fatalf("Write(%q) accepted", bad)
		}
		if string(mustRead(t, p)) != bad {
			t.Fatalf("%q was modified", bad)
		}
	}
}

func TestEmptyAndMissing(t *testing.T) {
	p := write(t, "  \n", 0o600)
	m, err := Read(p)
	if err != nil || len(m) != 0 {
		t.Fatalf("empty: %v %v", m, err)
	}
	if err := Write(p, map[string]any{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, p)); got != "{\n  \"k\": \"v\"\n}\n" {
		t.Fatalf("got %q", got)
	}
	_, err = Read(filepath.Join(t.TempDir(), "none.json"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if got, _ := Render(nil, map[string]any{}); string(got) != "{}\n" {
		t.Fatalf("empty object: %q", got)
	}
}

func TestTabIndentKept(t *testing.T) {
	p := write(t, "{\n\t\"a\": 1\n}\n", 0o600)
	m, _ := Read(p)
	m["b"] = map[string]any{"c": 2}
	if err := Write(p, m); err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, p)); got != "{\n\t\"a\": 1,\n\t\"b\": {\n\t\t\"c\": 2\n\t}\n}\n" {
		t.Fatalf("got %q", got)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// inOrder reports whether each needle occurs in s after the one before it.
func inOrder(s string, needles ...string) bool {
	last := -1
	for _, n := range needles {
		i := strings.Index(s, n)
		if i <= last {
			return false
		}
		last = i
	}
	return true
}
