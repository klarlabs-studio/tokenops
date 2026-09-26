// Package claudeusagemeter scrapes claude.ai's session-authenticated
// usage endpoint. This is the silver-bullet signal for Claude Max
// subscribers — same data Anthropic's own UI shows: session, aggregate
// weekly, and any model-specific utilization percentages plus reset
// timestamps. Window labels are vendor-owned and preserved as received.
// The cookie-scraping approach is undocumented and
// ToS-grey, but it is what every credible Claude usage tracker
// (Claude-Usage-Tracker, claude-bar variants) does, because no
// documented endpoint exposes Max-plan window state.
//
// Two-step flow:
//
//  1. GET claude.ai/api/organizations
//     → returns a JSON array of orgs the operator belongs to;
//     pick the first with `capabilities` including a Max/Pro entry.
//  2. GET claude.ai/api/organizations/{org_id}/usage
//     → returns the actual percentages.
//
// Operator UX: paste the `sessionKey` cookie from a browser devtools
// inspect (Application → Cookies → claude.ai). The cookie rolls every
// few weeks; the daemon logs WARN when it expires so the operator
// knows to re-paste.
package claudeusagemeter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

// OrgEntry is one row from /api/organizations.
type OrgEntry struct {
	UUID         string   `json:"uuid"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// UsageResponse is the part of /api/organizations/{org_id}/usage this
// meter reads. Every block is nullable, and absent means absent: a Claude
// Enterprise org can return null windows and report only extra_usage, while
// an org without metering returns null throughout.
// Decoding null as a zero-value struct is how this meter used to report
// "Anthropic says 0% used" for windows that do not exist.
type UsageResponse struct {
	FiveHour     *Window     `json:"five_hour"`
	SevenDay     *Window     `json:"seven_day"`
	SevenDayOpus *Window     `json:"seven_day_opus"`
	ExtraUsage   *ExtraUsage `json:"extra_usage"`
	// Windows preserves every top-level usage window under the label sent by
	// Anthropic. The named fields above remain compatibility aliases for the
	// two stable aggregate windows and the historical Opus window.
	Windows map[string]*Window `json:"-"`

	// Unrecognised names the blocks that were present but did not have
	// the shape this decoder reads. They are dropped rather than read as
	// zeros; the poller reports them.
	Unrecognised []string `json:"-"`
}

// UnmarshalJSON discovers window blocks by shape instead of maintaining a
// model-name allowlist. Anthropic can add or rename model-specific windows
// without silently discarding an otherwise readable vendor measurement.
func (u *UsageResponse) UnmarshalJSON(data []byte) error {
	type responseAlias UsageResponse
	var named responseAlias
	if err := json.Unmarshal(data, &named); err != nil {
		return err
	}
	*u = UsageResponse(named)
	u.Windows = make(map[string]*Window)

	var blocks map[string]json.RawMessage
	if err := json.Unmarshal(data, &blocks); err != nil {
		return err
	}
	for name, raw := range blocks {
		if name == "extra_usage" || name == "limits" || string(raw) == "null" {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			continue
		}
		if _, isWindow := fields["utilization"]; !isWindow {
			if likelyUsageWindowName(name) {
				u.Unrecognised = append(u.Unrecognised, name)
			}
			continue
		}
		if !validWindowName(name) {
			u.Unrecognised = append(u.Unrecognised, name)
			continue
		}
		var window Window
		if err := json.Unmarshal(raw, &window); err != nil {
			u.Unrecognised = append(u.Unrecognised, name)
			continue
		}
		u.Windows[name] = &window
	}
	u.syncNamedWindows()
	return nil
}

func likelyUsageWindowName(name string) bool {
	return name == "five_hour" || name == "seven_day" || strings.HasPrefix(name, "five_hour_") || strings.HasPrefix(name, "seven_day_")
}

func validWindowName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func (u *UsageResponse) syncNamedWindows() {
	u.FiveHour = u.Windows["five_hour"]
	u.SevenDay = u.Windows["seven_day"]
	u.SevenDayOpus = u.Windows["seven_day_opus"]
}

// Window is one rate-limit window's snapshot. Observed on a real Claude
// Max account:
//
//	"five_hour": {"utilization": 18, "resets_at": "2026-09-19T20:00:00.048431+00:00",
//	  "limit_dollars": null, "used_dollars": null, ...}
//
// A window without a readable utilization is reported as unrecognised,
// never read as 0%.
type Window struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

// ExtraUsage is the account's usage-billed allowance: on Claude Enterprise,
// the member's monthly spend limit and what has been spent against it.
// Amounts are in minor units of Currency, scaled by DecimalPlaces —
// monthly_limit 150000 with decimal_places 2 is 1500.00. Observed on a real
// Enterprise account:
//
//	"extra_usage": {"is_enabled": true, "monthly_limit": 150000,
//	  "used_credits": 109563, "utilization": 73.042, "currency": "USD",
//	  "decimal_places": 2, "spend_limit_reached": false, ...}
type ExtraUsage struct {
	IsEnabled         *bool    `json:"is_enabled"`
	MonthlyLimit      *float64 `json:"monthly_limit"`
	UsedCredits       *float64 `json:"used_credits"`
	Utilization       *float64 `json:"utilization"`
	Currency          string   `json:"currency"`
	DecimalPlaces     int      `json:"decimal_places"`
	SpendLimitReached bool     `json:"spend_limit_reached"`
}

// Amounts returns spend and limit in major units of Currency.
func (e *ExtraUsage) Amounts() (used, limit float64) {
	scale := math.Pow10(e.DecimalPlaces)
	return *e.UsedCredits / scale, *e.MonthlyLimit / scale
}

// HasSignal reports whether the response carries anything to meter.
func (u *UsageResponse) HasSignal() bool {
	return len(u.Windows) > 0 || u.ExtraUsage != nil
}

// Summary renders what Anthropic reported, one line per block, for an
// operator to check against claude.ai. Blocks it did not report are left
// out rather than shown as 0%.
func (u *UsageResponse) Summary() []string {
	var lines []string
	labels := make([]string, 0, len(u.Windows))
	for label := range u.Windows {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		lines = append(lines, fmt.Sprintf("%-16s %.1f%% used", label+":", *u.Windows[label].Utilization))
	}
	if e := u.ExtraUsage; e != nil {
		used, limit := e.Amounts()
		lines = append(lines, fmt.Sprintf("%-16s %.2f of %.2f %s this month (%.1f%%)",
			"Usage spend:", used, limit, e.Currency, used/limit*100))
	}
	return lines
}

// dropUnreadable nils every block present without the fields this decoder
// reads, and records its name, so no caller can mistake it for a reading.
func (u *UsageResponse) dropUnreadable() {
	for name, window := range u.Windows {
		if window.Utilization == nil {
			delete(u.Windows, name)
			u.Unrecognised = append(u.Unrecognised, name)
		}
	}
	u.syncNamedWindows()
	sort.Strings(u.Unrecognised)
	if e := u.ExtraUsage; e != nil {
		switch {
		case e.IsEnabled == nil:
			u.ExtraUsage = nil
			u.Unrecognised = append(u.Unrecognised, "extra_usage")
		case !*e.IsEnabled:
			// Present but switched off: nothing to meter, not unreadable.
			u.ExtraUsage = nil
		case e.MonthlyLimit == nil || e.UsedCredits == nil || *e.MonthlyLimit <= 0:
			u.ExtraUsage = nil
			u.Unrecognised = append(u.Unrecognised, "extra_usage")
		}
	}
}

// Client wraps the claude.ai cookie-authenticated endpoints.
type Client struct {
	HTTPClient *http.Client
	BaseURL    string
	SessionKey string
	// Clearance is Cloudflare's cf_clearance cookie from the browser the
	// session came from. claude.ai sits behind a bot check that answers
	// 403 "Just a moment..." to a client it does not recognise; that
	// cookie is the browser's proof of having passed it, and it is bound
	// to the browser's User-Agent, so the two travel together.
	Clearance string
	// UserAgent is that browser's own agent string. Empty falls back to a
	// plain Chrome string, which passes on its own only when the bot
	// check is not asking.
	UserAgent string
}

// ErrMissingCookie signals an empty session_key in config; poller
// stays idle and the CLI hint surfaces the fix.
var ErrMissingCookie = errors.New("claude-usage-meter: session_key required (paste from claude.ai devtools → Application → Cookies → sessionKey)")

// ErrUnauthorized indicates the cookie has expired or is invalid.
// Surfaced separately so the daemon can log a distinct WARN telling
// the operator to re-paste rather than burying the 401 in generic
// http error noise.
// ErrBotCheck means claude.ai's bot check refused the request. It is not a
// bad session: the browser's cf_clearance cookie is missing or has expired,
// and re-reading it from the browser is what fixes it.
var ErrBotCheck = errors.New("claude-usage-meter: claude.ai's bot check refused this request — the browser's cf_clearance cookie is missing or expired")

var ErrUnauthorized = errors.New("claude-usage-meter: claude.ai returned 401 — sessionKey likely expired, re-paste from devtools")

// NewClient binds a session cookie and returns a Client with sensible
// defaults. The UA mimics a recent Chrome to avoid Cloudflare bot
// challenges; without it the request 403s.
func NewClient(sessionKey string) *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		BaseURL:    "https://claude.ai",
		SessionKey: sessionKey,
	}
}

// Organizations fetches the list of orgs the cookie belongs to.
// Used to discover an org_id when the operator hasn't pinned one in
// config; with a single-org account this is a one-line resolution.
func (c *Client) Organizations(ctx context.Context) ([]OrgEntry, error) {
	if c.SessionKey == "" {
		return nil, ErrMissingCookie
	}
	body, err := c.get(ctx, "/api/organizations")
	if err != nil {
		return nil, err
	}
	var orgs []OrgEntry
	if err := json.Unmarshal(body, &orgs); err != nil {
		return nil, fmt.Errorf("claude-usage-meter: decode organizations: %w", err)
	}
	return orgs, nil
}

// Usage fetches the per-org usage snapshot.
func (c *Client) Usage(ctx context.Context, orgID string) (*UsageResponse, error) {
	if c.SessionKey == "" {
		return nil, ErrMissingCookie
	}
	if orgID == "" {
		return nil, fmt.Errorf("claude-usage-meter: org_id is required")
	}
	body, err := c.get(ctx, "/api/organizations/"+orgID+"/usage")
	if err != nil {
		return nil, err
	}
	var u UsageResponse
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("claude-usage-meter: decode usage: %w", err)
	}
	u.dropUnreadable()
	return &u, nil
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	if c.BaseURL == "" {
		c.BaseURL = "https://claude.ai"
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("claude-usage-meter: build request: %w", err)
	}
	cookie := "sessionKey=" + c.SessionKey
	if c.Clearance != "" {
		cookie += "; cf_clearance=" + c.Clearance
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Accept", "application/json")
	// Cloudflare in front of claude.ai 403s anything that doesn't look
	// like a browser. A vanilla Chrome UA is sufficient and tolerated
	// — we're identifying as the same caller every browser tab is.
	ua := c.UserAgent
	if ua == "" {
		ua = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	}
	req.Header.Set("User-Agent", ua)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("claude-usage-meter: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		if resp.StatusCode == http.StatusForbidden && isBotCheck(snippet) {
			return nil, fmt.Errorf("%w (%s)", ErrBotCheck, path)
		}
		return nil, fmt.Errorf("claude-usage-meter: %s: status %d: %s", path, resp.StatusCode, snippet)
	}
	return io.ReadAll(resp.Body)
}

// isBotCheck recognises Cloudflare's interstitial, which arrives as a 403
// carrying an HTML challenge page rather than an API error.
func isBotCheck(body []byte) bool {
	b := strings.ToLower(string(body))
	return strings.Contains(b, "just a moment") || strings.Contains(b, "cf-browser-verification") ||
		strings.Contains(b, "cloudflare")
}
