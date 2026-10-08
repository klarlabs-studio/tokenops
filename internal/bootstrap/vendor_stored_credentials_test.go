package bootstrap

import (
	"context"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type namedReader struct{ id string }

func (r namedReader) Endpoint() string               { return r.id }
func (r namedReader) Provider() eventschema.Provider { return eventschema.Provider(r.id) }
func (r namedReader) Source() string                 { return r.id + "-account" }
func (namedReader) Read(context.Context, string) (accounts.Reading, error) {
	return accounts.Reading{}, nil
}

// A stored key goes to its provider's reader; a session read from a
// browser is re-read from it only on demand, quietly, and never for a
// provider without a reader or with browser: none.
func TestStoredCredentials(t *testing.T) {
	cfg := config.Default()
	cfg.VendorUsage.Accounts.Credentials = map[string]config.AccountCredential{
		"acme":   {Key: "sk-acme"},
		"webby":  {Key: "sid=old", FromBrowser: true, Browser: "Firefox"},
		"nobody": {Key: "sk-orphan"},
		"off":    {FromBrowser: true, Browser: config.BrowserNone},
		"gw":     {Key: "vk", BaseURL: "https://gw.example"},
		"cloud":  {CredentialChain: true},
	}
	readers := []accounts.Reader{namedReader{"acme"}, namedReader{"webby"}, namedReader{"off"}, chainReaderFake{namedReader{"cloud"}}}
	cookieOf := func(id string) *providers.Cookie {
		if id == "webby" || id == "off" {
			return &providers.Cookie{Host: "webby.example", Names: []string{"sid", "cf"}}
		}
		return nil
	}
	var asked []string
	browser := func(_ context.Context, host string, names []string, only string) (map[string]string, error) {
		asked = append(asked, host+"|"+only)
		return map[string]string{"sid": "new", "cf": "clear"}, nil
	}
	got := storedCredentials(cfg, readers, cookieOf, browser)
	if len(asked) != 0 {
		t.Fatalf("the browser was read before it was needed: %v", asked)
	}
	if len(got) != 5 || got[0].Key != "sk-acme" || got[3].Key != "sid=old" || got[4].Resolve == nil || got[4].Endpoint != "webby" {
		t.Fatalf("got %+v", got)
	}
	// A credential chain is read only when it is needed, and nothing of it
	// was stored.
	if c := got[1]; c.Endpoint != "cloud" || c.Key != "" || c.Resolve == nil {
		t.Fatalf("chain credential %+v", c)
	}
	if key, err := got[1].Resolve(context.Background()); err != nil || key != "chain-key" {
		t.Errorf("chain resolved %q %v", key, err)
	}
	// A gateway's key is read at the address setup stored, by that gateway.
	if gw := got[2]; gw.Endpoint != accounts.GatewayEndpoint || gw.Gateway != "gw" || gw.BaseURL != "https://gw.example" || gw.Key != "vk" {
		t.Errorf("gateway credential %+v", gw)
	}
	key, err := got[4].Resolve(context.Background())
	if err != nil || key != "sid=new; cf=clear" || len(asked) != 1 || asked[0] != "webby.example|Firefox" {
		t.Errorf("resolved %q %v, asked %v", key, err, asked)
	}
}

type chainReaderFake struct{ namedReader }

func (chainReaderFake) Chain(context.Context) (string, string, error) {
	return "chain-key", "the environment", nil
}
