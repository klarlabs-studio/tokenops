package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/sessions"
)

// The cohort line says what the default left out, and, when every role is
// pooled, how many were the harness's own reviews.
func TestCohortLineSaysWhatWasPooled(t *testing.T) {
	for _, tc := range []struct {
		info sessions.CohortInfo
		want []string
		not  string
	}{
		{sessions.CohortInfo{Role: "primary", InWindow: 10, Selected: 6, ByRole: map[string]int{"primary": 6, "auto_review": 4}},
			[]string{"6 of the window's 10 instructions (role primary)"}, "automatic reviews"},
		{sessions.CohortInfo{Role: "all", InWindow: 10, Selected: 10, ByRole: map[string]int{"primary": 6, "auto_review": 4}},
			[]string{"4 of these are automatic reviews", "--role primary"}, "cohort:"},
	} {
		var b bytes.Buffer
		writeCohortLine(&b, &tc.info)
		for _, w := range tc.want {
			if !strings.Contains(b.String(), w) {
				t.Errorf("role %s: missing %q in %q", tc.info.Role, w, b.String())
			}
		}
		if strings.Contains(b.String(), tc.not) {
			t.Errorf("role %s: %q in %q", tc.info.Role, tc.not, b.String())
		}
	}
}
