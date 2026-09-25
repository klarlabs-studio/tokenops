package mcp

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/infra/daemonhint"
)

// preferredURL must surface the mDNS hostname when present and fall
// back to the loopback URL otherwise. This is the only place where
// the "which URL does the agent get?" decision lives — covering it
// explicitly prevents a future hostname channel (Tailscale, dynamic
// DNS) from being silently bypassed.
func TestPreferredURL(t *testing.T) {
	cases := []struct {
		name    string
		payload daemonhint.Payload
		want    string
	}{
		{"loopback only", daemonhint.Payload{URL: "http://127.0.0.1:7878"}, "http://127.0.0.1:7878"},
		{"mdns wins", daemonhint.Payload{URL: "http://127.0.0.1:7878", LocalURL: "http://tokenops.local:7878"}, "http://tokenops.local:7878"},
		{"empty mdns falls back", daemonhint.Payload{URL: "http://127.0.0.1:7878", LocalURL: ""}, "http://127.0.0.1:7878"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.payload.PreferredURL(); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}
