package biller

import "strings"

// Router is a service that chooses the model for each turn itself (ADR
// 0009 §5). When one is in the path, TokenOps does not choose the model
// for that harness; it governs the router instead.
type Router struct {
	// Name is the router's identifier, e.g. "firerouter".
	Name string `json:"name"`
	// Display is its name in words.
	Display string `json:"display"`
	// Source is where its behaviour is documented.
	Source string `json:"source,omitempty"`
}

// routers recognise a router from the model ID a harness asks for. The
// served model is no help: a router answers with whatever it chose.
var routers = []struct {
	match func(model string) bool
	r     Router
}{
	{func(m string) bool { return strings.Contains(m, "firerouter") },
		Router{Name: "firerouter", Display: "FireRouter", Source: "https://docs.fireworks.ai/nexus/firerouter"}},
	{func(m string) bool { return m == "openrouter/auto" },
		Router{Name: "openrouter-auto", Display: "OpenRouter Auto", Source: "https://openrouter.ai/docs/features/model-routing"}},
}

// RouterFor reports the router a requested model ID names, if any.
func RouterFor(model string) (Router, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return Router{}, false
	}
	for _, r := range routers {
		if r.match(m) {
			return r.r, true
		}
	}
	return Router{}, false
}
