package bootstrap

import (
	"net/url"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/spend/biller"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/opencode"
	"go.klarlabs.de/tokenops/internal/infra/harnesskeys"
)

// accountCredentials finds the keys the harnesses use and names the
// endpoint each is sent to, so an account reader only ever gets its own
// vendor's key.
func accountCredentials() []accounts.Credential {
	return credentialsFor(harnesskeys.Find(harnesskeys.Options{}))
}

func credentialsFor(found []harnesskeys.Credential) []accounts.Credential {
	out := make([]accounts.Credential, 0, len(found))
	for _, c := range found {
		endpoint := ""
		switch {
		case c.Endpoint != "":
			endpoint = c.Endpoint
		case c.BaseURL != "":
			// A base URL decides. An unknown host may be a gateway the
			// operator runs or subscribes to; the poller recognises it by
			// its health route before the key goes anywhere, and then
			// sends it only there.
			if _, ok := biller.EndpointFor(c.BaseURL); ok {
				endpoint = biller.EndpointName(c.BaseURL, "")
			} else if !unreadableGateway(c.BaseURL) {
				out = append(out, accounts.Credential{Endpoint: accounts.GatewayEndpoint, Origin: c.Origin, Key: c.Key, BaseURL: c.BaseURL})
				continue
			}
		case c.ProviderID != "":
			// Mainland-China platforms issue keys their international
			// counterparts refuse.
			if strings.HasSuffix(c.ProviderID, "-cn") {
				continue
			}
			_, endpoint, _ = opencode.Provider(c.ProviderID)
		}
		if endpoint == "" {
			continue
		}
		out = append(out, accounts.Credential{Endpoint: endpoint, Origin: c.Origin, Key: c.Key})
	}
	return out
}

// unreadableGateways are gateways whose key cannot read its own spend
// (Portkey, Cloudflare AI Gateway), so nothing is asked of them.
var unreadableGateways = []string{"api.portkey.ai", "gateway.ai.cloudflare.com"}

func unreadableGateway(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return true
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range unreadableGateways {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// fireworksFallbackKey is a Fireworks key a harness other than
// FireConnect uses (opencode's auth.json, Codex's config).
func fireworksFallbackKey() string {
	for _, c := range accountCredentials() {
		if c.Endpoint == "fireworks" {
			return c.Key
		}
	}
	return ""
}
