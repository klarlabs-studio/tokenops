package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	accountsapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/accounts"
)

// repoRoot is the repository root from this package.
const repoRoot = "../.."

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func repoFileExists(rel string) bool {
	_, err := os.Stat(filepath.Join(repoRoot, rel))
	return err == nil
}

// Every provider in the registry is complete: each source has a reader, a
// test fixture, a hint in `vendor-usage status` and a row in the docs; each
// provider has a name in the menu bar and a logo exactly when it says so.
// It is the test that ties together what used to be seventeen hand-kept
// lists, so a provider added in its own file cannot be half-wired (#517).
func TestEveryProviderIsComplete(t *testing.T) {
	readers := map[string]string{} // source tag -> provider
	scoped := map[string]bool{}    // source tag -> takes a scope
	for _, r := range accountsapi.Readers() {
		readers[r.Source()] = string(r.Provider())
		_, scoped[r.Source()] = r.(accounts.Scoped)
	}
	gateways := map[string]string{}
	for _, g := range accountsapi.Gateways() {
		gateways[g.Source()] = g.Name()
	}
	cfg := config.Default()
	cliDocs := readRepoFile(t, "web/docs/guide/cli.md")
	menubar := readRepoFile(t, "apps/menubar/providers_gen.go")

	for _, d := range providers.All() {
		id := string(d.ID)
		if !strings.Contains(menubar, `"`+id+`":`) {
			t.Errorf("%s: no menu-bar name; run go generate ./...", id)
		}
		logo := repoFileExists("apps/menubar/frontend/logos/" + id + ".svg")
		if logo != d.Logo {
			t.Errorf("%s: Logo is %v but logos/%s.svg exists = %v", id, d.Logo, id, logo)
		}
		for _, s := range d.Sources {
			checkReader(t, id, s, readers, gateways)
			if s.Reader == providers.AccountReader && (s.Scope != "") != scoped[s.Tag] {
				t.Errorf("%s/%s: the descriptor's Scope (%q) and the reader's WithScope disagree", id, s.Tag, s.Scope)
			}
			if cfg.VendorUsageConfigHint(s.Tag) == "" {
				t.Errorf("%s/%s: no hint in `vendor-usage status`", id, s.Tag)
			}
			if !strings.Contains(cliDocs, "`"+s.Tag+"`") {
				t.Errorf("%s/%s: no row in web/docs/guide/cli.md; run go generate ./...", id, s.Tag)
			}
		}
	}
	// And the other way: no reader runs that the registry does not know.
	known := map[string]bool{}
	for _, s := range providers.Sources() {
		known[s.Tag] = true
	}
	for tag := range readers {
		if !known[tag] {
			t.Errorf("account reader %s has no source in the provider registry", tag)
		}
	}
	for tag := range gateways {
		if !known[tag] {
			t.Errorf("gateway %s has no source in the provider registry", tag)
		}
	}
}

func checkReader(t *testing.T, id string, s providers.Source, readers, gateways map[string]string) {
	t.Helper()
	switch s.Reader {
	case providers.AccountReader:
		if readers[s.Tag] != id {
			t.Errorf("%s/%s: no reader in internal/infra/vendorusage/accounts reports this source for this provider", id, s.Tag)
		}
	case providers.GatewayReader:
		if gateways[s.Tag] != id {
			t.Errorf("%s/%s: no gateway in internal/infra/vendorusage/accounts reports this source", id, s.Tag)
		}
	case providers.BespokeReader:
		if !repoFileExists(s.Package) {
			t.Errorf("%s/%s: reader package %s does not exist", id, s.Tag, s.Package)
		}
	}
	fixture := s.Fixture
	if s.Reader != providers.BespokeReader {
		fixture = "internal/infra/vendorusage/accounts/testdata/" + id + ".json"
	}
	if !repoFileExists(fixture) {
		t.Errorf("%s/%s: no test fixture at %s", id, s.Tag, fixture)
	}
}
