package scanscope

import (
	"path/filepath"
	"testing"
)

// The directory names below are verbatim from a real ~/.claude/projects.
// The worksim ones are a simulation harness that produced 94% of a
// 7-day window and graded straight A's against nobody's work.
func TestEphemeralRecognisesScratchWork(t *testing.T) {
	for _, dir := range []string{
		"-private-var-folders-wz-yfymxbq52xvb15kg8khpnpdm0000gn-T-worksim-claudecode-1569245291",
		"-var-folders-wz-abc-T-somerun",
		"-tmp-scratch-clone",
		"-private-tmp-claude-501-somesession",
		"-var-tmp-build-123",
	} {
		if !Ephemeral(dir) {
			t.Errorf("Ephemeral(%q) = false, want true — that is a scratch directory", dir)
		}
	}
}

// The counterweight. A project is in scope because of where it lives,
// and real work lives in real places — including places whose names
// merely contain the same letters.
func TestEphemeralLeavesRealProjectsAlone(t *testing.T) {
	for _, dir := range []string{
		"-Users-felixgeelhaar-Developer-open-source-tokenops",
		"-home-me-src-api",
		"-Users-me-tmp-notes",       // "tmp" inside the path, not at its root
		"-Users-me-var-folders-app", // ditto
		"-tmpfile-service",          // starts with "-tmp" but is not "-tmp/"
		"proj",                      // a test fixture's project dir
		"",
	} {
		if Ephemeral(dir) {
			t.Errorf("Ephemeral(%q) = true, want false — that is real work", dir)
		}
	}
}

// ~/.claude/projects is itself under a temp root in every test that
// builds a fixture. The rule reads the project name, never the machine
// path, so a fixture is never mistaken for noise.
func TestEphemeralPathReadsTheProjectNotTheRoot(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "proj", "session.jsonl")
	if EphemeralPath(fixture) {
		t.Errorf("EphemeralPath(%q) = true — a fixture under the test temp dir is not scratch work", fixture)
	}
	real := filepath.Join(t.TempDir(), "-private-var-folders-x-T-worksim-1", "session.jsonl")
	if !EphemeralPath(real) {
		t.Errorf("EphemeralPath(%q) = false — the project name says scratch", real)
	}
}

func TestKeepPreservesOrderAndDropsScratch(t *testing.T) {
	in := []string{
		"/r/-Users-me-src-a/1.jsonl",
		"/r/-private-var-folders-x-T-worksim-9/2.jsonl",
		"/r/-Users-me-src-b/3.jsonl",
	}
	got := Keep(in)
	want := []string{in[0], in[2]}
	if len(got) != len(want) {
		t.Fatalf("Keep returned %d paths, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Keep()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Keep must not scribble on its input: callers reuse the slice.
	if in[1] != "/r/-private-var-folders-x-T-worksim-9/2.jsonl" {
		t.Error("Keep mutated the slice it was given")
	}
}
