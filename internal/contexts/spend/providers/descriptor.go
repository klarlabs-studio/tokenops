// Package providers is the registry of every usage and plan provider
// TokenOps knows: who bills a request, where its usage, balance or plan
// windows are read from, with which credential, what its plans are and
// what it is called on every surface.
//
// Each provider is one file, provider_<id>.go, holding one function
// provider<Name>() that returns its Descriptor. registry_gen.go lists those
// functions and is written by `go generate ./...`; nothing registers itself
// in init(). Every list that used to be kept by hand (the biller's
// endpoints, opencode's provider IDs, the environment variables a key is
// found in, models.dev's price IDs, the plan catalog, the vendor-usage
// sources and their hints, the display names on the cards and in the menu
// bar, the docs tables) is derived from here. docs/providers.md says how to
// add one.
//
// The package is pure data: it imports nothing but the event schema, so
// every domain package may read it. The readers live under
// internal/infra/vendorusage.
package providers

import (
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Descriptor is one provider: everything TokenOps knows about it that is
// data rather than code.
type Descriptor struct {
	// ID is the provider as events carry it ("anthropic", "zai"). It is
	// also the file's name: provider_<id>.go, with "-" as "_".
	ID eventschema.Provider
	// DisplayName is the name the provider's users know it by: "Claude"
	// for anthropic, "Codex" for openai. Cards, the menu bar and the docs
	// use it.
	DisplayName string
	// PlanPrefix is the vendor's name as its plans' display names start
	// with it ("Claude ", "ChatGPT "), so a card titled with DisplayName
	// does not repeat it. Empty for none.
	PlanPrefix string
	// Sources are the ways TokenOps reads this provider's usage, cheapest
	// and least invasive first (ADR 0011). A provider with none is in the
	// catalog only; CatalogOnly says why.
	Sources []Source
	// CatalogOnly explains a provider with no reader yet: its endpoints,
	// plans or prices are known, its usage is not read.
	CatalogOnly string
	// Endpoints are the hosts, or paths on them, whose billing is known
	// (ADR 0009). A base URL a harness points at one is billed to this
	// provider.
	Endpoints []Endpoint
	// Opencode are opencode's provider IDs (models.dev's) that bill to
	// this provider, each with the endpoint it names.
	Opencode []OpencodeID
	// EnvVars are the conventional environment variables a key for this
	// provider is found in. The key goes to the endpoint of the first
	// Opencode ID; a provider opencode does not know (no Opencode ID)
	// sends it to the endpoint named by its own ID.
	EnvVars []string
	// ModelsDev are the models.dev provider IDs whose per-token rates
	// price this provider's turns (ADR 0009 §6). A coding plan's own ID,
	// listed at $0, is not one: plan turns are valued at the vendor's
	// pay-as-you-go rate.
	ModelsDev []string
	// Plans are the provider's catalog plans. Provider is filled in from
	// ID when empty.
	Plans []Plan
	// Logo is true when apps/menubar/frontend/logos/<id>.svg is the
	// vendor's own mark; false shows two letters of DisplayName.
	Logo bool
	// Docs is what the generated docs say about the provider.
	Docs Docs
}

// Kind is what a source reads.
type Kind string

const (
	// Subscription is a plan's usage windows, as the vendor reports them.
	Subscription Kind = "subscription"
	// Balance is prepaid credit left.
	Balance Kind = "balance"
	// Spend is spend in a period, against a cap when there is one.
	Spend Kind = "spend"
	// Gateway is a gateway key's own budget.
	Gateway Kind = "gateway"
	// LocalLog is the records a harness keeps on disk: transcripts,
	// rollouts, a hook ledger, a status line feed.
	LocalLog Kind = "local-log"
)

// Credential is what a source reads with.
type Credential string

const (
	// APIKey is a key the operator's harnesses or environment already
	// send the vendor, or one typed into `tokenops vendor-usage setup`.
	APIKey Credential = "api-key"
	// AdminKey is an organisation admin key the operator mints.
	AdminKey Credential = "admin-key"
	// OAuthFile is another application's sign-in (Claude Code, the
	// Copilot IDE plugins), opt-in only (ADR 0011 §1.4).
	OAuthFile Credential = "oauth-file"
	// CLI is the vendor's own CLI signing its own request (codex
	// app-server).
	CLI Credential = "cli"
	// LocalFile is a file on disk; no secret.
	LocalFile Credential = "local-file"
	// BrowserCookie is a web session. It is read from the browser only in
	// the interactive `tokenops vendor-usage setup <id>`, which may show a
	// Keychain prompt; the daemon re-reads it quietly and never prompts.
	BrowserCookie Credential = "browser-cookie"
	// CredentialChain is the vendor's own standard credential chain on this
	// machine (AWS's environment variables and shared credentials file). It
	// is read only once `tokenops vendor-usage setup <id>` opted in, then as
	// the daemon polls; nothing secret is stored.
	CredentialChain Credential = "credential-chain"
	// AppKeychain is another application's sign-in kept in the macOS
	// Keychain (Zed's). Opt-in (ADR 0011 §1.4): only the interactive
	// `tokenops vendor-usage setup <id>` reads it, which may show a
	// Keychain prompt, and stores the token; the daemon never reads the
	// Keychain for it and never refreshes it.
	AppKeychain Credential = "app-keychain"
	// PasswordLogin is a session the vendor issues for a username and
	// password. `tokenops vendor-usage setup <id>` asks for both (the
	// password without echo), signs in once and stores only the session
	// token; the password is never stored or logged.
	PasswordLogin Credential = "password-login"
)

// Switch is what turns a source on.
type Switch string

const (
	// SwitchAccounts sources run with the vendor account readers
	// (vendor_usage.accounts, on unless switched off) and read only when a
	// key for the vendor is found.
	SwitchAccounts Switch = "accounts"
	// SwitchConfig sources have their own config block; internal/config
	// says how each is switched on and what its hint is.
	SwitchConfig Switch = "config"
	// SwitchAlways sources always run: they read something that is empty
	// until the operator installs what writes it.
	SwitchAlways Switch = "always"
)

// Reader names where a source's reader lives.
type Reader string

const (
	// AccountReader is an HTTP reader in internal/infra/vendorusage/accounts,
	// run by the generic account poller. Its fixture is
	// internal/infra/vendorusage/accounts/testdata/<id>.json.
	AccountReader Reader = "account"
	// GatewayReader is a gateway in the same package, recognised by its
	// health route before a key is sent.
	GatewayReader Reader = "gateway"
	// BespokeReader has its own poller, wired in internal/bootstrap; the
	// source's Package and Fixture name it.
	BespokeReader Reader = "bespoke"
)

// Verification says how far a reader has been checked.
type Verification string

const (
	// VerifiedLive readers have read a real account.
	VerifiedLive Verification = "live"
	// FromDocs readers were built from the vendor's public docs and
	// tested against fixtures only.
	FromDocs Verification = "docs"
	// FromClientSource readers were built from the vendor's own client
	// source, the endpoint being unpublished, and tested against fixtures.
	FromClientSource Verification = "client-source"
	// FromCodexBar readers were ported from CodexBar's provider source
	// (github.com/steipete/CodexBar), with the vendor's docs where it has
	// them, and tested against fixtures; never against a live account.
	FromCodexBar Verification = "codexbar"
)

// Source is one way a provider's usage is read. Its Tag is stamped on every
// event it stores.
type Source struct {
	// Name is the source as `vendor-usage status` lists it, usually its
	// config key ("openrouter_account").
	Name string
	// Tag is the event source tag ("openrouter-account").
	Tag  string
	Kind Kind
	// Credential is what it reads with.
	Credential Credential
	Switch     Switch
	Reader     Reader
	// Package is a bespoke reader's domain package, relative to the
	// repository root.
	Package string
	// Fixture is a bespoke reader's test fixture, relative to the
	// repository root. Account and gateway readers use
	// internal/infra/vendorusage/accounts/testdata/<provider id>.json.
	Fixture string
	// AnyProvider marks a source that reports on every provider (opencode's
	// store), so it grades every provider's headroom.
	AnyProvider bool
	// Cookie names the browser cookies a BrowserCookie source reads.
	Cookie *Cookie
	// KeychainServer is the internet-password server an AppKeychain
	// source's sign-in is kept under ("https://zed.dev"). The reader is
	// given it as "<account> <secret>".
	KeychainServer string
	// KeyFormat is what `tokenops vendor-usage setup` asks for when an
	// APIKey source's credential is more than one key, e.g.
	// "TEAM_ID:MANAGEMENT_KEY". Empty asks for the API key.
	KeyFormat string
	// EnvVars are the environment variables holding this source's own
	// credential when it is not the provider's API key (an organisation
	// admin key, OPENAI_ADMIN_KEY). A key found there is sent only to this
	// source's reader, never to another of the provider's endpoints.
	EnvVars []string
	// BaseURLEnv is, for a gateway, the variable naming its address
	// (SUB2API_BASE_URL); with a key in EnvVars it is read there, named,
	// without being recognised first. DefaultBaseURL is the hosted
	// service's address, used when BaseURLEnv is unset.
	BaseURLEnv     string
	DefaultBaseURL string
	// Verified says how far the reader has been checked.
	Verified Verification
	// Reference is, for a FromCodexBar reader, the CodexBar source it
	// follows ("CodexBar Sources/.../openai.js"), where a comment would
	// otherwise name it.
	Reference string
	// Endpoint, Shows and RecognisedBy are its row in the docs tables:
	// "`GET /api/v1/key`", "the key's spend, against its credit cap".
	// RecognisedBy is a gateway's health route.
	Endpoint     string
	Shows        string
	RecognisedBy string
}

// Cookie is the session cookies a web source reads: the cookies the
// browser sends to Host (or, when it holds none, to the first of Also that
// does). The reader is given them as one Cookie header value:
// "name=value; name2=value2".
type Cookie struct {
	Host string
	// Also are other hosts the session may be on, tried in order: another
	// region's console ("bailian.console.aliyun.com" after
	// "modelstudio.console.alibabacloud.com").
	Also []string
	// Names are the cookies read; a name ending in "*" is a prefix
	// ("ory_session_*").
	Names []string
	// Proof are the cookies that show someone is signed in; at least one
	// must be present. Empty: the first of Names.
	Proof []string
	// AllForHost reads every cookie the browser sends to the host, for a
	// vendor whose session cookie names are not known. Names, if any,
	// still order the header.
	AllForHost bool
	// PasteOnly is a session no browser is read for (the vendor's cookie
	// names are not known well enough): setup asks for the Cookie header.
	PasteOnly bool
}

// Hosts is Host followed by Also.
func (c Cookie) Hosts() []string { return append([]string{c.Host}, c.Also...) }

// Billing says how an endpoint bills the models it serves (ADR 0009).
type Billing string

const (
	// Direct is the model vendor's own API: it bills what it serves.
	Direct Billing = "direct"
	// Reseller serves and bills every model, closed ones included.
	Reseller Billing = "reseller"
	// OwnCredential serves and bills its own models, and runs closed
	// models on the operator's credential with their vendor.
	OwnCredential Billing = "own-credential"
)

// Endpoint is a known API host, or a path on one, and how it bills.
type Endpoint struct {
	Host string
	// Path narrows the endpoint to base URLs under it.
	Path    string
	Billing Billing
	// Name is the endpoint's name on each turn when it differs from the
	// provider: a vendor's pay-as-you-go API next to its plan is
	// "<provider>-api", so a bound plan covers only the plan's turns.
	Name string
	// Source pins where the billing behaviour is documented.
	Source string
}

// OpencodeID is one of opencode's provider IDs and the endpoint it names:
// "" for the vendor's own, as before endpoints were recorded.
type OpencodeID struct {
	ID       string
	Endpoint string
}

// Docs is a provider's text in the generated docs.
type Docs struct {
	// Label names the provider in the docs tables when DisplayName is not
	// specific enough: "Moonshot (Kimi API)".
	Label string
	// Setup says what the operator does, if anything.
	Setup string
	// CatalogLabel names the provider's coding plans where the plan
	// catalog is described ("z.ai GLM Coding"); empty leaves it out.
	CatalogLabel string
}

// Plan is a subscription in the plan catalog and its published limits.
// plans.Plan is this type.
type Plan struct {
	// Name is the catalog identifier used in config (e.g. "claude-max-20x").
	Name string
	// Provider matches eventschema.Provider so the spend engine can
	// route events to the right plan record.
	Provider string
	// Display is the human-readable plan name (e.g. "Claude Max").
	Display string
	// InputTokensPerMonth is the published monthly cap on input tokens.
	// Zero indicates the vendor publishes no fixed cap (rate-limit only).
	InputTokensPerMonth int64
	// OutputTokensPerMonth is the published monthly cap on output
	// tokens. Zero matches InputTokensPerMonth semantics.
	OutputTokensPerMonth int64
	// RequestsPerMonth is the cap on total requests, when published.
	RequestsPerMonth int64
	// RateLimitWindow is the shortest documented rate-limit window
	// (e.g. messages per 5 hours). Used by the headroom calculator to
	// warn before the window resets.
	RateLimitWindow time.Duration
	// MessagesPerWindow is the documented cap on user-facing units
	// (messages or premium requests) within RateLimitWindow. Zero
	// indicates the vendor publishes no concrete number (e.g.
	// "depends on conversation length"); headroom math then surfaces
	// raw consumption without a percentage.
	MessagesPerWindow int64
	// WindowUnit names the user-facing unit MessagesPerWindow counts
	// — "messages", "requests", or "premium_requests". Display only;
	// the consumption reader always counts whole PromptEvents.
	WindowUnit string
	// RelativeTo names the plan this tier's allowance is defined against,
	// and Multiplier is how many times that plan's per-window allowance it
	// receives.
	//
	// Anthropic documents Max and Team only this way — "five times the Pro
	// plan's per-session usage allowance" — and no longer publishes an
	// absolute for any tier, Pro included. Holding three independent
	// absolutes meant they could drift out of the relationship the vendor
	// actually states, and they had: Pro 45 against Max 5x 50 is 1.1x
	// where the documentation says 5x, with both Max entries citing a
	// support URL that now 404s.
	//
	// Deriving leaves exactly one number that can go stale, and correcting
	// it corrects every tier at once. A relative entry carries no
	// MessagesPerWindow of its own; Lookup computes it.
	RelativeTo string
	Multiplier float64
	// SpendDenominated marks a plan whose limit is money, not a
	// rate-limit window.
	//
	// Usage-based Enterprise is billed at API rates from the first token:
	// there is no cap to be under, so there is no percentage to report and
	// no window to have headroom in. What it has instead is a spend limit
	// the org's admins set in the vendor console — a number the operator
	// knows and this catalog never could. So the plan declares that its
	// denominator is supplied, and binding it without one is refused
	// rather than defaulted.
	SpendDenominated bool
	// SourceURL pins the vendor page that documents these limits. Drift
	// surfaces in PR review when the URL or numbers change.
	SourceURL string
	// MonthlyUSD is the published list price per month on monthly
	// billing, in US dollars before tax; per seat when PerSeat. Zero means
	// no flat price is published (Enterprise: seats plus usage) or the
	// tier is not specific enough to have one.
	MonthlyUSD float64
	PerSeat    bool
	// PriceSource pins the vendor page the price was read from, with the
	// date it was read.
	PriceSource string
	// VendorPlanTypes are the plan identifiers the vendor itself reports
	// for this plan (Codex's rate-limit `plan_type`), so a binding that
	// disagrees with what the vendor says can be flagged. Only values seen
	// in Codex logs are listed ("pro" is what the $200 plan reported before
	// the cheaper tiers existed); a tier never seen reporting one has none.
	VendorPlanTypes []string
}
