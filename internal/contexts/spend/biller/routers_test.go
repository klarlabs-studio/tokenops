package biller

import "testing"

func TestRouterFor(t *testing.T) {
	for model, want := range map[string]string{
		"firerouter": "firerouter",
		"accounts/fireworks/routers/firerouter-coder": "firerouter",
		"openrouter/auto":           "openrouter-auto",
		"claude-opus-5-5":           "",
		"anthropic/claude-sonnet-5": "",
		"":                          "",
	} {
		r, ok := RouterFor(model)
		if ok != (want != "") || r.Name != want {
			t.Errorf("%q → %+v %v, want %q", model, r, ok, want)
		}
	}
}
