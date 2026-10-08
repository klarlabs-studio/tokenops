package providers

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/tools/gen"
)

// registry_gen.go lists every provider_*.go file's function; a provider
// added without regenerating it would silently not exist.
func TestRegistryListIsCurrent(t *testing.T) {
	want, err := gen.ListFile(".", "registry_gen.go", []gen.List{{Var: "registered", Prefix: "provider", Type: "Descriptor"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("registry_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("registry_gen.go is stale; run: go generate ./internal/contexts/spend/providers")
	}
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Each provider is in its own file, named after it, with one function.
func TestOneFilePerProvider(t *testing.T) {
	for _, d := range All() {
		file := "provider_" + strings.ReplaceAll(string(d.ID), "-", "_") + ".go"
		if _, err := os.Stat(file); err != nil {
			t.Errorf("provider %q is not in %s", d.ID, file)
		}
	}
	files, _ := filepath.Glob("provider_*.go")
	if len(files) != len(All()) {
		t.Errorf("%d provider files for %d providers: one file, one provider", len(files), len(All()))
	}
}

// The registry's invariants: every derived list is a function of it, so a
// duplicate or a hole here is a wrong answer somewhere else.
func TestRegistryIsConsistent(t *testing.T) {
	ids, tags, names, plans, opencode, env, hosts := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	prev := ""
	for _, d := range All() {
		id := string(d.ID)
		switch {
		case !idPattern.MatchString(id):
			t.Errorf("provider ID %q: lower case, digits and dashes only", id)
		case ids[id]:
			t.Errorf("provider %q registered twice", id)
		case strings.ReplaceAll(id, "-", "_") < strings.ReplaceAll(prev, "-", "_"):
			t.Errorf("provider %q after %q: registry_gen.go is by file name", id, prev)
		}
		ids[id], prev = true, id
		if d.DisplayName == "" {
			t.Errorf("%s: no DisplayName", id)
		}
		if (len(d.Sources) == 0) != (d.CatalogOnly != "") {
			t.Errorf("%s: a provider has sources or says why not (CatalogOnly), not both", id)
		}
		if len(d.EnvVars) > 0 && len(d.Opencode) == 0 && len(d.Sources) == 0 {
			t.Errorf("%s: a variable with no Opencode ID goes to the provider's own reader; it has none", id)
		}
		for _, s := range d.Sources {
			checkSource(t, id, s, tags, names)
			for _, v := range s.EnvVars {
				if env[v] {
					t.Errorf("%s/%s: %s claimed twice", id, s.Tag, v)
				}
				env[v] = true
			}
		}
		for _, p := range d.Plans {
			if p.Name == "" || plans[p.Name] {
				t.Errorf("%s: plan %q empty or listed twice", id, p.Name)
			}
			plans[p.Name] = true
			if p.Provider != id {
				t.Errorf("%s: plan %q is for provider %q", id, p.Name, p.Provider)
			}
		}
		for _, o := range d.Opencode {
			if opencode[o.ID] {
				t.Errorf("%s: opencode ID %q claimed twice", id, o.ID)
			}
			opencode[o.ID] = true
		}
		for _, v := range d.EnvVars {
			if env[v] {
				t.Errorf("%s: %s claimed twice", id, v)
			}
			env[v] = true
		}
		for _, e := range d.Endpoints {
			k := e.Host + e.Path
			if hosts[k] {
				t.Errorf("%s: endpoint %s claimed twice", id, k)
			}
			hosts[k] = true
			switch e.Billing {
			case Direct, Reseller, OwnCredential:
			default:
				t.Errorf("%s: endpoint %s has billing %q", id, k, e.Billing)
			}
		}
	}
}

func checkSource(t *testing.T, id string, s Source, tags, names map[string]bool) {
	t.Helper()
	if s.Name == "" || s.Tag == "" || tags[s.Tag] || names[s.Name] {
		t.Errorf("%s: source %q/%q empty or listed twice", id, s.Name, s.Tag)
	}
	tags[s.Tag], names[s.Name] = true, true
	switch s.Kind {
	case Subscription, Balance, Spend, Gateway, LocalLog:
	default:
		t.Errorf("%s/%s: kind %q", id, s.Tag, s.Kind)
	}
	switch s.Credential {
	case APIKey, AdminKey, OAuthFile, CLI, LocalFile, BrowserCookie, CredentialChain:
	case AppKeychain:
		if s.KeychainServer == "" {
			t.Errorf("%s/%s: an app-keychain source names the item's server", id, s.Tag)
		}
	default:
		t.Errorf("%s/%s: credential %q", id, s.Tag, s.Credential)
	}
	switch s.Switch {
	case SwitchAccounts, SwitchConfig, SwitchAlways:
	default:
		t.Errorf("%s/%s: switch %q", id, s.Tag, s.Switch)
	}
	switch s.Verified {
	case VerifiedLive, FromDocs, FromClientSource, FromCodexBar:
	default:
		t.Errorf("%s/%s: verification %q", id, s.Tag, s.Verified)
	}
	switch s.Reader {
	case AccountReader, GatewayReader:
		if s.Switch != SwitchAccounts {
			t.Errorf("%s/%s: an account or gateway reader runs with the account readers (SwitchAccounts)", id, s.Tag)
		}
		if s.Endpoint == "" || s.Shows == "" {
			t.Errorf("%s/%s: Endpoint and Shows make its docs row", id, s.Tag)
		}
		if s.Reader == GatewayReader && (s.Kind != Gateway || s.RecognisedBy == "") {
			t.Errorf("%s/%s: a gateway is Kind Gateway and says what recognises it", id, s.Tag)
		}
	case BespokeReader:
		if s.Package == "" || s.Fixture == "" {
			t.Errorf("%s/%s: a bespoke reader names its Package and Fixture", id, s.Tag)
		}
	default:
		t.Errorf("%s/%s: reader %q", id, s.Tag, s.Reader)
	}
	if s.Credential == BrowserCookie && s.Reader == AccountReader && (s.Cookie == nil || s.Cookie.Host == "" || len(s.Cookie.Names) == 0) {
		t.Errorf("%s/%s: a browser-session reader names its cookies", id, s.Tag)
	}
}

// The derived lists agree with the descriptors they come from.
func TestDerivedLists(t *testing.T) {
	if got := SourceProviders()["zai-account"]; got != "zai" {
		t.Errorf("zai-account reports on %q", got)
	}
	if _, ok := SourceProviders()["opencode"]; ok {
		t.Error("opencode's store reports on every provider, not on opencode")
	}
	if got := EnvVars()["MOONSHOT_API_KEY"]; got != "moonshotai" {
		t.Errorf("MOONSHOT_API_KEY is for %q", got)
	}
	if got := OwnEnvVars()["CODEBUFF_API_KEY"]; got != "codebuff" {
		t.Errorf("CODEBUFF_API_KEY is for %q", got)
	}
	if _, ok := OwnEnvVars()["MOONSHOT_API_KEY"]; ok {
		t.Error("a variable of a provider opencode knows goes through its opencode ID")
	}
	if got := Opencode()["zai"]; got.Provider != "zai" || got.Endpoint != "zai-api" {
		t.Errorf("opencode zai = %+v", got)
	}
	if got := ModelsDevPricing()["moonshotai"]; len(got) != 2 {
		t.Errorf("moonshotai prices %v, want moonshot and kimi", got)
	}
	if DisplayName("anthropic") != "Claude" || DisplayName("acme") != "Acme" || DisplayName("") != "" {
		t.Error("DisplayName")
	}
	if s, ok := ForSource("claude-usage-meter"); !ok || s.Provider != "anthropic" {
		t.Errorf("ForSource = %+v %v", s, ok)
	}
	d, _ := Lookup("openrouter")
	if s, ok := d.Setupable(); !ok || s.Tag != "openrouter-account" {
		t.Errorf("openrouter setup source = %+v %v", s, ok)
	}
	if d.Kind() != Spend || d.Label() != "OpenRouter" {
		t.Errorf("openrouter kind %q label %q", d.Kind(), d.Label())
	}
}
