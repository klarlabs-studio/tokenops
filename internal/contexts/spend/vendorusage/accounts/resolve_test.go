package accounts

import (
	"context"
	"testing"
)

// A credential read on demand (a browser session) is read only when no
// credential before it was accepted.
func TestPollerResolvesALazyCredentialOnlyWhenNeeded(t *testing.T) {
	resolved := 0
	lazy := Credential{Endpoint: "acme", Resolve: func(context.Context) (string, error) {
		resolved++
		return "sk-browser", nil
	}}
	r := &fakeReader{endpoint: "acme", good: "sk-stored", reading: Reading{Scope: "account", BalanceUSD: 1, HasBalance: true}}
	p := NewPoller(&captureBus{}, PollerOptions{Readers: []Reader{r}, Credentials: func() []Credential {
		return []Credential{{Endpoint: "acme", Key: "sk-stored"}, lazy}
	}})
	p.Scan(context.Background())
	if resolved != 0 {
		t.Fatalf("resolved %d times while the stored key worked", resolved)
	}
	r.good = "sk-browser"
	p.Scan(context.Background())
	if resolved != 1 || r.keys[len(r.keys)-1] != "sk-browser" {
		t.Fatalf("resolved %d, keys %v", resolved, r.keys)
	}
}
