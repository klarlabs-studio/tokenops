package daemon

import (
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
		case c.BaseURL != "":
			// A base URL decides; an unknown host names no vendor.
			if _, ok := biller.EndpointFor(c.BaseURL); ok {
				endpoint = biller.EndpointName(c.BaseURL, "")
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
