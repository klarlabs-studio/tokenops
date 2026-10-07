package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestCoachOutputCharacterization pins `tokenops coach prompts` and
// `tokenops coach replies` over the sessions fixture byte for byte, so
// moving their transcript reads onto capability/sessions cannot change
// them.
func TestCoachOutputCharacterization(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := writeSessionsFixture(t, filepath.Join(home, ".claude", "projects"))

	pinned := []string{"--root", root, "--source", "claude-code", "--since", "2026-01-01T00:00:00Z"}
	cases := []struct {
		golden string
		args   []string
	}{
		{"coach/prompts.txt", append([]string{"coach", "prompts"}, pinned...)},
		{"coach/prompts.json", append([]string{"coach", "prompts", "--json"}, pinned...)},
		{"coach/replies.txt", append([]string{"coach", "replies"}, pinned...)},
		{"coach/replies.json", append([]string{"coach", "replies", "--json"}, pinned...)},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			out, err := executeRoot(t, tc.args...)
			if err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			out = strings.ReplaceAll(out, home, "<HOME>")
			if strings.HasSuffix(tc.golden, ".json") {
				out = roundFloats(out)
			}
			assertGolden(t, tc.golden, out)
		})
	}
}
