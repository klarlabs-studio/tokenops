package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestEvalOutputCharacterization pins `tokenops eval` over the bundled
// suites, so moving it onto a capability cannot change it. The text
// report ranged over maps, so it is run several times: one order, every
// time.
func TestEvalOutputCharacterization(t *testing.T) {
	baseline := filepath.Join(t.TempDir(), "baseline.json")
	if _, err := executeRoot(t, "eval", "--output", baseline); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		golden string
		args   []string
	}{
		{"eval/report.txt", []string{"eval"}},
		{"eval/report.json", []string{"eval", "--json"}},
		{"eval/gated.txt", []string{"eval", "--baseline", baseline}},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			for range 5 {
				out, err := executeRoot(t, tc.args...)
				if err != nil {
					t.Fatalf("%v: %v", tc.args, err)
				}
				if strings.HasSuffix(tc.golden, ".json") {
					out = roundFloats(out)
				}
				assertGolden(t, tc.golden, out)
			}
		})
	}
}
