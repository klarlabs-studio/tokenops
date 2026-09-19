// Package claudeusagemeter scrapes claude.ai's session-authenticated
// usage endpoint. This is the silver-bullet signal for Claude Max
// subscribers — same data Anthropic's own UI shows: 5-hour, weekly
// all-models, and weekly Opus utilization percentages plus the reset
// timestamps. The cookie-scraping approach is undocumented and
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
// Enterprise org returns null for all three windows and reports only
// extra_usage, while a personal chat-only org returns null throughout.
// Decoding null as a zero-value struct is how this meter used to report
// "Anthropic says 0% used" for windows that do not exist.
type UsageResponse struct {
	FiveHour     *Window     `json:"five_hour"`
	SevenDay     *Window     `json:"seven_day"`
	SevenDayOpus *Window     `json:"seven_day_opus"`
	ExtraUsage   *ExtraUsage `json:"extra_usage"`

	// Unrecognised names the blocks that were present but did not have
	// the shape this decoder reads. They are dropped rather than read as
	// zeros; the poller reports them.
	Unrecognised []string `json:"-"`
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
	return u.FiveHour != nil || u.SevenDay != nil || u.SevenDayOpus != nil || u.ExtraUsage != nil
}

// Summary renders what Anthropic reported, one line per block, for an
// operator to check against claude.ai. Blocks it did not report are left
// out rather than shown as 0%.
func (u *UsageResponse) Summary() []string {
	var lines []string
	for _, w := range []struct {
		label string
		win   *Window
	}{{"5-hour window", u.FiveHour}, {"7-day window", u.SevenDay}, {"7-day (Opus)", u.SevenDayOpus}} {
		if w.win != nil {
			lines = append(lines, fmt.Sprintf("%-16s %.1f%% used", w.label+":", *w.win.Utilization))
		}
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
	for _, w := range []struct {
		name string
		win  **Window
	}{{"five_hour", &u.FiveHour}, {"seven_day", &u.SevenDay}, {"seven_day_opus", &u.SevenDayOpus}} {
		if *w.win != nil && (*w.win).Utilization == nil {
			*w.win = nil
			u.Unrecognised = append(u.Unrecognised, w.name)
		}
	}
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
}

// ErrMissingCookie signals an empty session_key in config; poller
// stays idle and the CLI hint surfaces the fix.
var ErrMissingCookie = errors.New("claude-usage-meter: session_key required (paste from claude.ai devtools → Application → Cookies → sessionKey)")

// ErrUnauthorized indicates the cookie has expired or is invalid.
// Surfaced separately so the daemon can log a distinct WARN telling
// the operator to re-paste rather than burying the 401 in generic
// http error noise.
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
	req.Header.Set("Cookie", "sessionKey="+c.SessionKey)
	req.Header.Set("Accept", "application/json")
	// Cloudflare in front of claude.ai 403s anything that doesn't look
	// like a browser. A vanilla Chrome UA is sufficient and tolerated
	// — we're identifying as the same caller every browser tab is.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
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
		return nil, fmt.Errorf("claude-usage-meter: %s: status %d: %s", path, resp.StatusCode, snippet)
	}
	return io.ReadAll(resp.Body)
}
