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
	}
	readers := []accounts.Reader{namedReader{"acme"}, namedReader{"webby"}, namedReader{"off"}}
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
	if len(got) != 3 || got[0].Key != "sk-acme" || got[1].Key != "sid=old" || got[2].Resolve == nil || got[2].Endpoint != "webby" {
		t.Fatalf("got %+v", got)
	}
	key, err := got[2].Resolve(context.Background())
	if err != nil || key != "sid=new; cf=clear" || len(asked) != 1 || asked[0] != "webby.example|Firefox" {
		t.Errorf("resolved %q %v, asked %v", key, err, asked)
	}
}

// A session whose cookies are not known by name was pasted, and is never
// re-read from a browser; one read with named cookies may be.
func TestRegistryCookie(t *testing.T) {
	if c := registryCookie("manus"); c == nil || c.Host != "manus.im" {
		t.Errorf("manus = %+v", c)
	}
	for _, id := range []string{"t3chat", "openrouter", "nope"} {
		if c := registryCookie(id); c != nil {
			t.Errorf("%s = %+v", id, c)
		}
	}
}
