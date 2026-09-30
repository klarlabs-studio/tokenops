package modelpolicy

import (
	"reflect"
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*opus*", "claude-opus-5", true},
		{"*OPUS*", "Claude-Opus-5", true},
		{"claude-haiku-*", "claude-haiku-4-5-20251001", true},
		{"claude-haiku-*", "claude-sonnet-5", false},
		{"openai/*", "openai/gpt-5", true},
		{"openai/*", "anthropic/claude-opus-5", false},
		{"*", "anything/at/all", true},
		{"gpt-?", "gpt-5", true},
		{"gpt-?", "gpt-55", false},
		{"exact", "exact", true},
		{"exact", "exactly", false},
		{"a*b*c", "axxbyyc", true},
		{"a*b*c", "axxbyy", false},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.name); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestCheck(t *testing.T) {
	anthropic, openai := eventschema.ProviderAnthropic, eventschema.ProviderOpenAI
	cases := []struct {
		name     string
		policy   Policy
		provider eventschema.Provider
		model    string
		want     bool
		reason   string
	}{
		{"empty policy permits", Policy{}, anthropic, "claude-opus-5", true, ""},
		{"deny by family", Policy{Deny: []string{"*opus*"}}, anthropic, "claude-opus-5", false, `denied by "*opus*"`},
		{"deny by vendor", Policy{Deny: []string{"openai/*"}}, openai, "gpt-5", false, `denied by "openai/*"`},
		{"deny by vendor spares others", Policy{Deny: []string{"openai/*"}}, anthropic, "claude-opus-5", true, ""},
		{"allow list admits", Policy{Allow: []string{"anthropic/*"}}, anthropic, "claude-haiku-4-5", true, ""},
		{"allow list excludes", Policy{Allow: []string{"anthropic/*"}}, openai, "gpt-5", false, "not on the allow list"},
		{"deny beats allow", Policy{Allow: []string{"anthropic/*"}, Deny: []string{"*opus*"}}, anthropic, "claude-opus-5", false, `denied by "*opus*"`},
		{"agent alias is matched too", Policy{Deny: []string{"*opus*"}}, anthropic, "opus", false, `denied by "*opus*"`},
		{"empty model is not ruled on", Policy{Allow: []string{"x"}}, anthropic, "", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := c.policy.Check(c.provider, c.model)
			if v.Permitted != c.want || v.Reason != c.reason {
				t.Fatalf("Check = %+v, want permitted=%v reason=%q", v, c.want, c.reason)
			}
		})
	}
}

func TestFilter(t *testing.T) {
	p := Policy{Deny: []string{"*opus*"}}
	got := p.Filter(eventschema.ProviderAnthropic, []string{"claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5"})
	if want := []string{"claude-sonnet-5", "claude-haiku-4-5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Filter = %v, want %v", got, want)
	}
}

func TestValidate(t *testing.T) {
	if err := (Policy{Deny: []string{" "}}).Validate(); err == nil {
		t.Fatal("blank pattern accepted")
	}
	if err := (Policy{Allow: []string{"anthropic/*"}, Deny: []string{"*opus*"}}).Validate(); err != nil {
		t.Fatal(err)
	}
}
