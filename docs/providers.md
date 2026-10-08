# Adding a usage or plan provider

A provider is everything TokenOps knows about one vendor: who bills a
request, where its usage, balance or plan windows are read, with which
credential, what its plans are, and what it is called on every surface. All
of that lives in **one descriptor file**. Every list that used to be kept by
hand is derived from the registry of descriptors: the biller's endpoints,
opencode's provider IDs, the environment variables a key is found in,
models.dev's price IDs, the plan catalog, the `vendor-usage status` rows and
their hints, the names on plan cards and in the menu bar, and the provider
tables in the docs. A test fails when any of them is missing.

Read ADR 0009 (routes, routers, billers), ADR 0010 (the daemon API serves
every surface) and ADR 0011 (several sources per provider) first.

## The recipe

For a provider read over HTTP with an API key or a browser session, which is
almost every one (`<id>` is the provider ID events carry, lower case with
dashes; `<Name>` is its Go name):

1. **Descriptor**: `internal/contexts/spend/providers/provider_<id>.go`
   (dashes as underscores), with one function `provider<Name>() Descriptor`.
   Copy `provider_openrouter.go` (spend), `provider_deepseek.go` (balance) or
   `provider_kimi.go` (subscription windows).
2. **Reader**: `internal/infra/vendorusage/accounts/<id>.go`, with the reader
   type and one function `reader<Name>() usage.Reader`. Copy `openrouter.go`.
   For a gateway read at the operator's own address, write `gateway<Name>()
   usage.Gateway` instead (copy `litellm.go`).
3. **Test and fixture**: `internal/infra/vendorusage/accounts/<id>_test.go`
   and `testdata/<id>.json`, the vendor's documented example answer or its
   own client's fixture. Never a live answer with real account data.
4. **Logo** (optional): `apps/menubar/frontend/logos/<id>.svg`, a plain SVG
   (no script, no `href`), from Lobe Icons as `logos/NOTICE.md` describes;
   set `Logo: true`. Without one the menu bar shows two letters.
5. **Generate**: `go generate ./...` (with `GOTOOLCHAIN=go1.26.8`). It
   rewrites `registry_gen.go`, `readers_gen.go`, the docs tables between
   `<!-- begin generated: ... -->` markers in `web/docs/guide/cli.md` and
   `configuration.md`, and the menu bar's `frontend/providers.js` and
   `providers_gen.go`.
6. **Verify**: `go test ./internal/contexts/spend/providers/
   ./internal/infra/vendorusage/accounts/ ./internal/bootstrap/
   ./internal/tools/gen/providercatalog/ ./internal/config/` and
   `cd apps/menubar && go test ./...`; before pushing, the repository's full
   checks (`gofmt -l .`, `golangci-lint run ./...`, `go test ./...`,
   `go test -count=1 ./internal/archlint`).
7. **Changelog**: one line under `## Unreleased` → `### Added`.

Nothing else is edited: no CLI command, no config block, no hint, no name
map. The generic `tokenops vendor-usage setup <id>` connects it, the account
poller reads it, and its hint comes from the descriptor. Generated files
conflict only where two providers sort next to each other; re-run
`go generate ./...` after a rebase to resolve them.

## The descriptor

```go
func providerAcme() Descriptor {
	return Descriptor{
		ID:          "acme",
		DisplayName: "Acme",            // as its users name it
		PlanPrefix:  "Acme ",           // when plan names start with it
		Logo:        true,              // logos/acme.svg ships
		Sources: []Source{{
			Name: "acme_account", Tag: "acme-account",
			Kind: Subscription, Credential: APIKey,
			Switch: SwitchAccounts, Reader: AccountReader,
			Verified: FromDocs,
			Endpoint: "`GET /v1/usage`", Shows: "5-hour and weekly windows",
		}},
		Endpoints: []Endpoint{{Host: "api.acme.ai", Billing: Direct, Source: "https://docs.acme.ai/..."}},
		Opencode:  []OpencodeID{{ID: "acme", Endpoint: "acme"}},
		EnvVars:   []string{"ACME_API_KEY"},
		ModelsDev: []string{"acme"},
		Plans:     []Plan{ /* catalog entries; Provider is filled in */ },
		Docs:      Docs{Label: "Acme Coding Plan", CatalogLabel: "Acme Coding"},
	}
}
```

| Field | What it drives |
|---|---|
| `ID` | the provider on every event, the file name, the setup argument |
| `DisplayName`, `PlanPrefix` | plan cards, menu bar, docs |
| `Sources[].Name`, `.Tag` | `vendor-usage status`, freshness, retention keys, the headroom signal |
| `Sources[].Kind` | which docs table: `Balance`/`Spend`, `Subscription`, `Gateway`, `LocalLog` |
| `Sources[].Credential` | `APIKey`, `BrowserCookie` (both set up generically), `AdminKey`, `OAuthFile`, `CLI`, `LocalFile` |
| `Sources[].Switch` | `SwitchAccounts` for account and gateway readers; `SwitchConfig` only for a reader with its own config block (add it to `configSwitches` in `internal/config/vendor_usage_sources.go` and a hint in `vendorusage_hints.go`) |
| `Sources[].Reader` | `AccountReader`, `GatewayReader`, or `BespokeReader` with `Package` and `Fixture` |
| `Sources[].Cookie` | the cookies a `BrowserCookie` source reads |
| `Sources[].KeyFormat` | what setup asks for when the credential is more than one key (`TEAM_ID:MANAGEMENT_KEY`) |
| `Sources[].Verified` | `VerifiedLive` only after a real account was read; else `FromDocs`, `FromClientSource`, or `FromCodexBar` for a reader ported from CodexBar's provider source (name the CodexBar path in a comment) |
| `Endpoints` | which base URLs bill to it (biller) |
| `Opencode` | opencode's provider IDs, with the endpoint each names (`"<id>-api"` for a pay-as-you-go API beside a plan) |
| `EnvVars` | where a key is found without setup; it goes to the endpoint of the first `Opencode` ID, or, with none, to the reader of the provider's own ID |
| `ModelsDev` | models.dev IDs whose per-token prices value its turns |
| `Plans` | the plan catalog (`tokenops plan list`) |
| `CatalogOnly` | instead of `Sources`: why nothing reads it yet |

## Readings

A reader implements `usage.Reader`
(`internal/contexts/spend/vendorusage/accounts`):

```go
Endpoint() string               // the endpoint whose keys it uses: the provider ID
Provider() eventschema.Provider // the provider ID
Source() string                 // the source tag
Read(ctx context.Context, key string) (usage.Reading, error)
```

`Read` calls only the vendor's own endpoint, with `getJSON` (bearer key),
`getJSONAuth` (another `Authorization` form) or your own request through
the helpers in `http.go`, and returns a `usage.Reading`:

- **Subscription windows**: `Subscription: true` and `Windows`, each a
  `usage.Window{Name, UsedPct, Duration, ResetsAt}`. `UsedPct` is the share
  *used*, 0–100; turn a vendor's "remaining" or ratio into it. Name windows
  by length with `windowName` ("5h", "week", "month").
- **Spend against a cap** (`extra_usage_*` attributes): `UsedUSD` with
  `HasUsed`, `LimitUSD` (0 for none), `LimitReached` when the vendor says
  requests are blocked. Headroom binds such a provider as `pay-as-you-go`.
- **Balance**: `BalanceUSD` with `HasBalance` (`balance_usd`); a balance in
  the vendor's own unit (Poe's points) is `Credits` with `CreditsUnit` and
  `HasCredits` (`balance_credits`), never converted to dollars.
- `Scope` says what the figures cover: `"key"`, `"account"`, `"team"`.

An empty reading (`Reading.Empty()`) is not stored. A refused key is
`usage.ErrAuth` (wrap it: `fmt.Errorf("%w (...)", usage.ErrAuth)`), including
a vendor that answers 200 with the refusal in the body; anything else is an
ordinary error. Never log or return the key; errors name the host and path,
never the query string.

## Credentials

The account poller tries, for each reader, every credential found for its
endpoint, in order, until one is accepted:

1. what `tokenops vendor-usage setup <id>` stored in
   `vendor_usage.accounts.credentials.<id>` (redacted wherever config is
   shown, like the claude.ai session);
2. the keys the harnesses already send that vendor (Claude Code's settings,
   Codex's `model_providers`, opencode's `auth.json` and config), found by
   `internal/infra/harnesskeys`;
3. the descriptor's `EnvVars`.

A key goes only to the reader of the endpoint it was found for.

### Browser-session providers

A vendor with no API for its usage but a web dashboard is read with the
browser's session cookies, the way the claude.ai meter is
(`internal/cli/cookie_setup.go`, `internal/bootstrap/vendor_pollers.go`
`browserSessionSource`):

- Set `Credential: BrowserCookie` and `Cookie: &Cookie{Host: "acme.ai",
  Names: []string{"session", "cf_clearance"}}`. The reader receives them as
  one Cookie header value, `session=...; cf_clearance=...`, and sends it as
  the `Cookie` header.
- **Only `tokenops vendor-usage setup <id>` reads the browser
  interactively** (`browsercookie.KeychainSecret`), after saying which
  Keychain item macOS will ask about. `--paste` types the header instead;
  `--browser` picks one browser.
- **The daemon never prompts.** It re-reads a session first read from a
  browser (`from_browser: true`) only when the stored one is refused, with
  `browsercookie.QuietSecret()`; when macOS would ask, the read fails and
  the source's health says so. `keychain.disabled` reads no Keychain at all
  (`DisabledSecret`), and `browser: none` never reads a browser. A pasted
  session is never re-read from a browser.
- No agent-facing (MCP) tool reads a browser.

## Checklist

- [ ] `provider_<id>.go` with one `provider<Name>()`; `Verified` honest
- [ ] `<id>.go` reader with `reader<Name>()` (or `gateway<Name>()`), calling only the vendor's documented or client-used endpoint
- [ ] `<id>_test.go` and `testdata/<id>.json`; a refused-key case returns `usage.ErrAuth`
- [ ] logo SVG and `Logo: true`, or neither
- [ ] `go generate ./...` run; generated files committed
- [ ] the tests above pass, including `TestEveryProviderIsComplete` (`internal/bootstrap`) and `TestRegistryIsConsistent`
- [ ] no live call with a real key in any test
- [ ] CHANGELOG line under `## Unreleased`
