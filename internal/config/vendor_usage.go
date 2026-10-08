package config

import (
	"time"
)

// VendorUsageConfig wires the vendor-side usage pollers. Each provider
// has its own block because authentication, polling cadence, and the
// signal quality story differ. ClaudeCode reads the local Claude Code
// stats cache; future blocks (anthropic admin API, openai usage)
// add here as separate sub-structs.
type VendorUsageConfig struct {
	ClaudeCode       ClaudeCodeUsageConfig      `yaml:"claude_code"`
	ClaudeCodeJSONL  ClaudeCodeJSONLUsageConfig `yaml:"claude_code_jsonl"`
	CodexJSONL       CodexJSONLUsageConfig      `yaml:"codex_jsonl"`
	OpenCode         OpenCodeUsageConfig        `yaml:"opencode"`
	GeminiCLI        GeminiCLIUsageConfig       `yaml:"gemini_cli"`
	Pi               PiUsageConfig              `yaml:"pi"`
	Anthropic        AnthropicUsageConfig       `yaml:"anthropic"`
	GitHubCopilot    GitHubCopilotUsageConfig   `yaml:"github_copilot"`
	Cursor           CursorUsageConfig          `yaml:"cursor"`
	ClaudeUsageMeter ClaudeUsageMeterConfig     `yaml:"claude_usage_meter"`
	ClaudeCodeOAuth  ClaudeCodeOAuthConfig      `yaml:"claude_code_oauth"`
	CodexAppServer   CodexAppServerConfig       `yaml:"codex_app_server"`
	Fireworks        FireworksUsageConfig       `yaml:"fireworks"`
	Accounts         AccountsUsageConfig        `yaml:"accounts"`
}

// AccountsUsageConfig wires the vendor account readers (ADR 0009 §7):
// OpenRouter's key usage and cap, DeepSeek's and Moonshot's prepaid
// balance, each read with the key a harness already sends that vendor.
// On unless Enabled is false; a vendor with no key on the machine is not
// called. Interval defaults to 15 minutes.
type AccountsUsageConfig struct {
	Enabled  *bool         `yaml:"enabled,omitempty"`
	Interval time.Duration `yaml:"interval,omitempty"`
	// Credentials are the keys and browser sessions `tokenops vendor-usage
	// setup <provider>` stored, by provider ID, for a vendor no harness
	// holds a key for. They are read before the harnesses' keys, sent only
	// to that vendor, and redacted wherever configuration is shown.
	Credentials map[string]AccountCredential `yaml:"credentials,omitempty"`
}

// AccountCredential is one stored vendor credential.
type AccountCredential struct {
	// Key is the API key, or the session's Cookie header value.
	Key string `yaml:"key,omitempty"`
	// FromBrowser re-reads the session from the browser as the daemon
	// polls, quietly: when macOS would ask, the read is skipped. Browser
	// limits it to one browser by name.
	FromBrowser bool   `yaml:"from_browser,omitempty"`
	Browser     string `yaml:"browser,omitempty"`
	// BaseURL is a gateway's address, where the key is read: setup stores
	// it for a gateway the operator runs or subscribes to.
	BaseURL string `yaml:"base_url,omitempty"`
	// CredentialChain reads the vendor's own credential chain on this
	// machine (AWS's environment and shared credentials file) as the
	// daemon polls: setup opted in; nothing secret is stored.
	CredentialChain bool `yaml:"credential_chain,omitempty"`
}

// On reports whether the readers run: unless switched off.
func (c AccountsUsageConfig) On() bool { return c.Enabled == nil || *c.Enabled }

// FireworksUsageConfig wires the Fireworks account reader (ADR 0009 §7):
// spend this month against the account's or the member's cap, read from
// Fireworks' API with the key FireConnect or FIREWORKS_API_KEY already
// provides. It is on unless Enabled is false, and idle on a machine with
// no Fireworks key. Interval defaults to 15 minutes.
type FireworksUsageConfig struct {
	Enabled  *bool         `yaml:"enabled,omitempty"`
	Interval time.Duration `yaml:"interval,omitempty"`
}

// On reports whether the reader runs: unless switched off.
func (c FireworksUsageConfig) On() bool { return c.Enabled == nil || *c.Enabled }

// GitHubCopilotUsageConfig wires the api.github.com/copilot_internal/user
// poller. OAuthToken empty → poller reads it from
// ~/.config/github-copilot/apps.json (or hosts.json) — same file the
// IDE plugins use. Interval defaults to 2 minutes.
type GitHubCopilotUsageConfig struct {
	Enabled    bool          `yaml:"enabled"`
	OAuthToken string        `yaml:"oauth_token"`
	Interval   time.Duration `yaml:"interval"`
}

// CursorUsageConfig wires the cursor.com/api/usage poller. Cookie +
// UserID must be set (extract from the Cursor IDE devtools, or via a
// future state.vscdb auto-discovery). Empty means the poller stays
// idle. Interval defaults to 2 minutes.
type CursorUsageConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Cookie   string        `yaml:"cookie"`
	UserID   string        `yaml:"user_id"`
	Interval time.Duration `yaml:"interval"`
}

// ClaudeUsageMeterConfig wires the claude.ai usage-meter
// poller. SessionKey extracted from the operator's browser (devtools
// → Application → Cookies → sessionKey). OrgID empty → poller
// resolves it via /api/organizations on first scan. Interval defaults
// to 5 minutes; Anthropic's cookie tier rate-limits aggressive
// polling and the data shifts on a 5-hour bucket cadence anyway.
type ClaudeUsageMeterConfig struct {
	Enabled    bool   `yaml:"enabled"`
	SessionKey string `yaml:"session_key"`
	// Clearance and UserAgent are an optional, matched Cloudflare session.
	// They let installations that cannot read a browser's protected cookie
	// database use a deliberately copied, content-free usage request instead.
	Clearance string `yaml:"clearance,omitempty"`
	UserAgent string `yaml:"user_agent,omitempty"`
	// BrowserHeaders is a strict, non-secret subset of browser request
	// metadata captured by --paste-request. Cloudflare may bind a clearance
	// session to these client hints as well as the User-Agent. Arbitrary
	// copied headers are never persisted.
	BrowserHeaders map[string]string `yaml:"browser_headers,omitempty"`
	// BrowserCookies contains only Cloudflare's short-lived bot-management
	// cookies captured by --paste-request. Values are credentials and must be
	// redacted anywhere configuration is displayed.
	BrowserCookies map[string]string `yaml:"browser_cookies,omitempty"`
	OrgID          string            `yaml:"org_id"`
	Interval       time.Duration     `yaml:"interval"`
	// FromBrowser re-reads the session and Cloudflare clearance cookies
	// from the local browser as the daemon polls. claude.ai's bot check
	// refuses a request without a clearance cookie, and that cookie
	// expires within hours: a meter that stored one at setup works for an
	// afternoon and is refused thereafter. macOS asks to allow the
	// keychain read once per installed version.
	FromBrowser bool `yaml:"from_browser,omitempty"`
	// Browser limits that read to one browser by name; empty searches,
	// and BrowserNone never reads one. Only a session read from a browser
	// (FromBrowser) is read from it again; a pasted one never makes the
	// daemon open a browser's Keychain item.
	Browser string `yaml:"browser,omitempty"`
}

// BrowserNone keeps the claude.ai meter from ever reading a browser.
const BrowserNone = "none"

// CodexAppServerConfig asks Codex for its plan windows through `codex
// app-server` (ADR 0011): Codex signs its own request, so TokenOps reads no
// credential. On unless Enabled is false, and idle when no codex binary is
// found. Path overrides where codex is; Interval defaults to 15 minutes.
type CodexAppServerConfig struct {
	Enabled  *bool         `yaml:"enabled,omitempty"`
	Path     string        `yaml:"path,omitempty"`
	Interval time.Duration `yaml:"interval,omitempty"`
}

// On reports whether the source runs.
func (c CodexAppServerConfig) On() bool { return c.Enabled == nil || *c.Enabled }

// ClaudeCodeOAuthConfig reads Claude's plan windows with Claude Code's own
// sign-in (ADR 0011, opt-in). The token belongs to Claude Code: it is read
// from ~/.claude/.credentials.json, or from the macOS Keychain only when
// Keychain is set, held in memory, sent only to api.anthropic.com, and
// never refreshed. Interval defaults to 10 minutes.
type ClaudeCodeOAuthConfig struct {
	Enabled bool `yaml:"enabled"`
	// Keychain allows reading Claude Code's Keychain item on macOS, set by
	// `setup claude-code --keychain`. The daemon reads it quietly: when
	// macOS would ask (after Claude Code renews its token, or an upgrade),
	// it skips the read rather than prompt.
	Keychain bool          `yaml:"keychain,omitempty"`
	Interval time.Duration `yaml:"interval,omitempty"`
}

// CodexJSONLUsageConfig enables the Codex CLI session-log reader.
// Parses ~/.codex/sessions/<yyyy>/<mm>/<dd>/rollout-*.jsonl. Empty
// Root defaults to ~/.codex/sessions.
type CodexJSONLUsageConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Root     string        `yaml:"root"`
	Interval time.Duration `yaml:"interval"`
}

// OpenCodeUsageConfig enables the opencode session reader, which opens
// opencode's SQLite store read-only and surfaces per-assistant-turn token
// usage. Empty Root defaults to ~/.local/share/opencode/opencode.db
// (honouring XDG_DATA_HOME).
type OpenCodeUsageConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Root     string        `yaml:"root"`
	Interval time.Duration `yaml:"interval"`
}

// GeminiCLIUsageConfig enables the Gemini CLI reader, which parses the
// chat recordings under ~/.gemini/tmp/*/chats for each model turn's
// tokens. Empty Root defaults to that directory (GEMINI_CLI_HOME honoured).
type GeminiCLIUsageConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Root     string        `yaml:"root"`
	Interval time.Duration `yaml:"interval"`
}

// PiUsageConfig enables the Pi transcript reader, which parses the
// sessions Pi keeps under ~/.pi/agent/sessions (and its fork OMP under
// ~/.omp/agent/sessions) for each assistant turn's tokens, under the
// provider that served it. Empty Root reads both (PI_CODING_AGENT_DIR
// and PI_CODING_AGENT_SESSION_DIR honoured).
type PiUsageConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Root     string        `yaml:"root"`
	Interval time.Duration `yaml:"interval"`
}

// ClaudeCodeJSONLUsageConfig enables the per-turn JSONL reader that
// parses ~/.claude/projects/**/*.jsonl. This is the high-confidence
// successor to the v0.10.2 stats-cache reader (which lags by days on
// active users). Empty Root defaults to ~/.claude/projects.
type ClaudeCodeJSONLUsageConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Root     string        `yaml:"root"`
	Interval time.Duration `yaml:"interval"`
}

// AnthropicUsageConfig wires the Anthropic Admin API poller. AdminKey
// must be a sk-ant-admin-* key minted in the Claude Console; without
// one the poller stays idle but the daemon still starts. Interval
// defaults to 5 minutes (the API freshness lag).
type AnthropicUsageConfig struct {
	Enabled     bool          `yaml:"enabled"`
	AdminKey    string        `yaml:"admin_key"`
	Interval    time.Duration `yaml:"interval"`
	BucketWidth string        `yaml:"bucket_width"`
}

// ClaudeCodeUsageConfig enables reading ~/.claude/stats-cache.json and
// emitting envelopes for the per-model daily token totals Claude Code
// records there. Empty Path defaults to the conventional location.
// Interval below 15s is clamped at the poller level to avoid
// hammering the file on caches that rotate frequently.
type ClaudeCodeUsageConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Path     string        `yaml:"path"`
	Interval time.Duration `yaml:"interval"`
}
