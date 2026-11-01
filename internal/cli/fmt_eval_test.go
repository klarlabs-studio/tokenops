package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/fmtinsight"
)

func isolateFmtEval(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

func TestFmtEvalLeadsWithTheInvariant(t *testing.T) {
	isolateFmtEval(t)
	out, err := executeRoot(t, "fmt", "eval")
	if err != nil {
		t.Fatalf("fmt eval: %v\n%s", err, out)
	}
	inv := strings.Index(out, "What fmt never drops")
	recall := strings.Index(out, "Evidence recall")
	if inv < 0 || recall < 0 || inv > recall {
		t.Fatalf("the invariant must come before recall:\n%s", out)
	}
	for _, want := range []string{
		"critical items recovered",
		"FORMATTER",
		"go ", "kubectl", "generic",
		"TOTAL",
		"tokenops fmt experiment",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestFmtEvalJSON(t *testing.T) {
	isolateFmtEval(t)
	out, err := executeRoot(t, "fmt", "eval", "--json")
	if err != nil {
		t.Fatalf("fmt eval --json: %v", err)
	}
	var rep fmtinsight.RecallReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if rep.Invariant == "" || len(rep.Levels) != 3 || len(rep.Levels[1].Formatters) == 0 {
		t.Errorf("report = %+v", rep)
	}
}

// A corpus that loses a critical item fails the command, so the eval can
// gate an operator's own labeled captures in CI.
func TestFmtEvalFailsOnACriticalMiss(t *testing.T) {
	isolateFmtEval(t)
	dir := t.TempDir()
	// yarn's formatter drops "warning" lines at balanced; labeling one
	// critical makes it a critical miss.
	raw := "yarn install v1.22.22\n[1/4] Resolving packages...\nwarning \"a > b\" has unmet peer dependency\nsuccess Saved lockfile.\nDone in 1.00s.\n"
	label := `{"evidence":[{"kind":"error_message","text":"has unmet peer dependency"}]}`
	if err := os.WriteFile(filepath.Join(dir, "yarn.peer.txt"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yarn.peer.json"), []byte(label), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := executeRoot(t, "fmt", "eval", "--corpus", dir)
	if err == nil || !strings.Contains(err.Error(), "critical") {
		t.Fatalf("err = %v, want a critical-miss failure\n%s", err, out)
	}
	if !strings.Contains(out, "yarn.peer") {
		t.Errorf("the miss is not named:\n%s", out)
	}
}
