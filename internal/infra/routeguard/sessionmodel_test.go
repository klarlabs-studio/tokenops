package routeguard

import (
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// New sessions step one tier down and no further; a menu the policy has
// thinned leaves them where they are rather than jump two tiers.
func TestSessionModelTarget(t *testing.T) {
	all := []string{"claude-haiku-4-5", "claude-sonnet-5", "claude-opus-5", "claude-fable-5-1"}
	for _, tc := range []struct {
		name, current string
		candidates    []string
		want          string
	}{
		{"opus to sonnet", "claude-opus-5", all, "sonnet"},
		{"fable to opus", "claude-fable-5-1", all, "opus"},
		{"sonnet stays", "claude-sonnet-5", all, ""},
		{"haiku stays", "claude-haiku-4-5", all, ""},
		{"policy rules sonnet out", "claude-opus-5", []string{"claude-haiku-4-5", "claude-opus-5", "claude-fable-5-1"}, ""},
		{"unknown model", "some-model", all, ""},
		{"no session model", "", all, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, alias, _ := SessionModelTarget(tc.current, eventschema.ProviderAnthropic, catalog(), tc.candidates)
			if alias != tc.want {
				t.Errorf("alias %q, want %q", alias, tc.want)
			}
		})
	}
}
