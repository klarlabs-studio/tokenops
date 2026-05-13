package detect

import (
	"io/fs"
	"path/filepath"
	"testing"
	"time"
)

// fakeEnv implements Env without touching the real filesystem.
type fakeEnv struct {
	home  string
	envs  map[string]string
	dirs  map[string]bool
	files map[string]bool
}

type fakeInfo struct {
	name string
	dir  bool
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return 0 }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.dir }
func (f fakeInfo) Sys() any           { return nil }

func (e fakeEnv) HomeDir() (string, error) { return e.home, nil }
func (e fakeEnv) Getenv(k string) string   { return e.envs[k] }
func (e fakeEnv) Stat(path string) (fs.FileInfo, error) {
	if e.dirs[path] {
		return fakeInfo{name: path, dir: true}, nil
	}
	if e.files[path] {
		return fakeInfo{name: path, dir: false}, nil
	}
	return nil, fs.ErrNotExist
}

func TestDetectClaudeCodeDirReturnsAnthropicMedium(t *testing.T) {
	home := filepath.FromSlash("/Users/test")
	env := fakeEnv{
		home: home,
		dirs: map[string]bool{filepath.Join(home, ".claude"): true},
	}
	out := Detect(env)
	if len(out) == 0 {
		t.Fatal("expected at least one detection")
	}
	got := out[0]
	if got.Provider != "anthropic" || got.Confidence != ConfidenceMedium {
		t.Errorf("got %+v want anthropic/medium", got)
	}
	if got.Plan != "" {
		t.Errorf("plan should be empty (ambiguous tier); got %q", got.Plan)
	}
}

func TestDetectCursorReturnsCursorMedium(t *testing.T) {
	home := filepath.FromSlash("/Users/test")
	env := fakeEnv{
		home: home,
		dirs: map[string]bool{filepath.Join(home, ".cursor"): true},
	}
	out := Detect(env)
	found := false
	for _, d := range out {
		if d.Provider == "cursor" && d.Confidence == ConfidenceMedium {
			found = true
		}
	}
	if !found {
		t.Errorf("expected cursor detection; got %+v", out)
	}
}

func TestDetectAPIKeysReturnLow(t *testing.T) {
	env := fakeEnv{
		home: filepath.FromSlash("/Users/test"),
		envs: map[string]string{"OPENAI_API_KEY": "sk-abc"},
	}
	out := Detect(env)
	if len(out) != 1 {
		t.Fatalf("got %d detections, want 1", len(out))
	}
	d := out[0]
	if d.Provider != "openai" || d.Confidence != ConfidenceLow {
		t.Errorf("got %+v want openai/low", d)
	}
}

func TestDetectNoSignalsReturnsEmpty(t *testing.T) {
	env := fakeEnv{home: filepath.FromSlash("/Users/test")}
	if out := Detect(env); len(out) != 0 {
		t.Errorf("expected empty, got %v", out)
	}
}

func TestDetectMultipleSortedByConfidence(t *testing.T) {
	home := filepath.FromSlash("/Users/test")
	env := fakeEnv{
		home: home,
		dirs: map[string]bool{
			filepath.Join(home, ".claude"): true,
			filepath.Join(home, ".cursor"): true,
		},
		envs: map[string]string{"ANTHROPIC_API_KEY": "sk-x"},
	}
	out := Detect(env)
	if len(out) < 3 {
		t.Fatalf("expected >=3 detections, got %d", len(out))
	}
	if out[0].Confidence != ConfidenceMedium {
		t.Errorf("first should be medium, got %v", out[0].Confidence)
	}
	last := out[len(out)-1]
	if last.Confidence != ConfidenceLow {
		t.Errorf("last should be low (API key), got %v", last.Confidence)
	}
}
