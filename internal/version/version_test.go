package version

import (
	"strings"
	"testing"
)

func TestStringContainsAllFields(t *testing.T) {
	got := String()
	for _, want := range []string{Version, Commit, Date} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
}
