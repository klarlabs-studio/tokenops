// Package providercatalog presents the provider registry
// (internal/contexts/spend/providers) to the surfaces that cannot read it
// at run time: the docs site's tables and the menu bar, a separate Go
// module that may not import internal packages. Its output is generated
// into checked-in files by `go generate ./...` (internal/tools/gen/providerdocs),
// and a test fails when a checked-in file differs from what the registry
// says.
package providercatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"sort"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
)

// Region is a generated region of a Markdown file, between the markers
// gen.BeginMarker(Name) and gen.EndMarker(Name).
type Region struct {
	// File is relative to the repository root.
	File string
	Name string
	Body string
}

// File is a whole generated file, relative to the repository root.
type File struct {
	Path string
	Body []byte
}

const (
	cliDocs    = "web/docs/guide/cli.md"
	configDocs = "web/docs/guide/configuration.md"
	// MenubarJS and MenubarGo are the menu bar's provider names and logos.
	MenubarJS = "apps/menubar/frontend/providers.js"
	MenubarGo = "apps/menubar/providers_gen.go"
	// LogoDir holds each vendor's logo as <id>.svg.
	LogoDir = "apps/menubar/frontend/logos"
)

// Regions are the docs tables the registry generates.
func Regions() []Region {
	return []Region{
		{File: cliDocs, Name: "provider-accounts", Body: accountTable(providers.Balance, providers.Spend)},
		{File: cliDocs, Name: "provider-subscriptions", Body: accountTable(providers.Subscription)},
		{File: cliDocs, Name: "provider-gateways", Body: gatewayTable()},
		{File: cliDocs, Name: "provider-env-vars", Body: envVars()},
		{File: cliDocs, Name: "provider-list", Body: providerList()},
		{File: configDocs, Name: "provider-plan-catalog", Body: planCatalog()},
	}
}

// Files are the menu bar's generated files.
func Files() ([]File, error) {
	goSrc, err := menubarGo()
	if err != nil {
		return nil, err
	}
	return []File{{Path: MenubarJS, Body: menubarJS()}, {Path: MenubarGo, Body: goSrc}}, nil
}

func accountTable(kinds ...providers.Kind) string {
	var b strings.Builder
	b.WriteString("| Vendor | Endpoint | Shows |\n|---|---|---|\n")
	for _, d := range providers.All() {
		for _, s := range d.Sources {
			if s.Reader != providers.AccountReader || !hasKind(kinds, s.Kind) {
				continue
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", d.Label(), s.Endpoint, s.Shows)
		}
	}
	return b.String()
}

func hasKind(kinds []providers.Kind, k providers.Kind) bool {
	for _, x := range kinds {
		if x == k {
			return true
		}
	}
	return false
}

func gatewayTable() string {
	var b strings.Builder
	b.WriteString("| Gateway | Recognised by | Shows |\n|---|---|---|\n")
	for _, d := range providers.All() {
		for _, s := range d.Sources {
			if s.Reader == providers.GatewayReader {
				fmt.Fprintf(&b, "| %s | %s | %s: %s |\n", d.Label(), s.RecognisedBy, s.Endpoint, s.Shows)
			}
		}
	}
	return b.String()
}

func envVars() string {
	env := providers.EnvVars()
	vars := make([]string, 0, len(env))
	for v := range env {
		vars = append(vars, v)
	}
	sort.Strings(vars)
	for i, v := range vars {
		vars[i] = "`" + v + "`"
	}
	return "The environment variables read for a key: " + strings.Join(vars, ", ") + "."
}

// providerList is every provider: what is read, with what, and how far the
// reader has been checked.
func providerList() string {
	var b strings.Builder
	b.WriteString("| Provider | Source | Reads | Credential | Checked |\n|---|---|---|---|---|\n")
	for _, d := range providers.All() {
		if len(d.Sources) == 0 {
			fmt.Fprintf(&b, "| %s | — | in the catalog only: %s | — | — |\n", d.DisplayName, d.CatalogOnly)
			continue
		}
		for i, s := range d.Sources {
			name := d.DisplayName
			if i > 0 {
				name = ""
			}
			fmt.Fprintf(&b, "| %s | `%s` | %s | %s | %s |\n", name, s.Tag, s.Shows, credential(s.Credential), verified(s.Verified))
		}
	}
	return b.String()
}

func credential(c providers.Credential) string {
	switch c {
	case providers.APIKey:
		return "API key"
	case providers.AdminKey:
		return "admin key"
	case providers.OAuthFile:
		return "another app's sign-in"
	case providers.CLI:
		return "the vendor's CLI"
	case providers.LocalFile:
		return "local files"
	case providers.BrowserCookie:
		return "browser session"
	}
	return string(c)
}

func verified(v providers.Verification) string {
	switch v {
	case providers.VerifiedLive:
		return "against a real account"
	case providers.FromDocs:
		return "public docs and fixtures"
	case providers.FromClientSource:
		return "the vendor's client source and fixtures"
	case providers.FromCodexBar:
		return "CodexBar's source, the vendor's docs and fixtures"
	}
	return string(v)
}

func planCatalog() string {
	var labels []string
	for _, d := range providers.All() {
		if d.Docs.CatalogLabel != "" {
			labels = append(labels, d.Docs.CatalogLabel)
		}
	}
	if len(labels) == 0 {
		return ""
	}
	list := labels[0]
	if len(labels) > 1 {
		list = strings.Join(labels[:len(labels)-1], ", ") + " and " + labels[len(labels)-1]
	}
	return "Coding plans are in the catalog: " + list + "."
}

// menuProvider is a provider as the menu bar shows it.
type menuProvider struct {
	Name string `json:"name"`
	Logo bool   `json:"logo"`
}

func menuProviders() map[string]menuProvider {
	out := map[string]menuProvider{}
	for _, d := range providers.All() {
		out[string(d.ID)] = menuProvider{Name: d.DisplayName, Logo: d.Logo}
	}
	return out
}

func menubarJS() []byte {
	m := menuProviders()
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b bytes.Buffer
	b.WriteString("// Code generated by internal/tools/gen/providerdocs from the provider registry; DO NOT EDIT.\n")
	b.WriteString("// Each provider's name and whether logos/<id>.svg is its logo.\n")
	b.WriteString("window.TOKENOPS_PROVIDERS = {\n")
	for i, id := range ids {
		v, _ := json.Marshal(m[id])
		k, _ := json.Marshal(id)
		sep := ","
		if i == len(ids)-1 {
			sep = ""
		}
		fmt.Fprintf(&b, "  %s: %s%s\n", k, v, sep)
	}
	b.WriteString("};\n")
	return b.Bytes()
}

func menubarGo() ([]byte, error) {
	m := menuProviders()
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b bytes.Buffer
	b.WriteString("// Code generated by internal/tools/gen/providerdocs from the provider registry; DO NOT EDIT.\n\n")
	b.WriteString("package main\n\n")
	b.WriteString("// providerNames are short names for the menu bar.\n")
	b.WriteString("var providerNames = map[string]string{\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "\t%q: %q,\n", id, m[id].Name)
	}
	b.WriteString("}\n")
	return format.Source(b.Bytes())
}
