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
//
// The package holds the usage snapshot, the poller, Connect and the mapping
// to envelopes. The cookie-authenticated HTTP client lives in
// internal/infra/vendorusage/claudeusagemeter and reaches them through the
// UsageClient and SessionClient ports.
package claudeusagemeter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// UsageClient reads claude.ai's organizations and their usage. The HTTP
// client in internal/infra/vendorusage/claudeusagemeter satisfies it.
type UsageClient interface {
	Organizations(ctx context.Context) ([]OrgEntry, error)
	Usage(ctx context.Context, orgID string) (*UsageResponse, error)
}

// SessionClient is a UsageClient whose browser session the poller can
// read and replace, so a refused request can be retried with the session
// the browser holds now.
type SessionClient interface {
	UsageClient
	// Session is the session the client sends; Browser is not tracked.
	Session() Session
	// SetSession replaces the key, clearance and user agent it sends.
	SetSession(Session)
}

// ClientConfig is what the poller builds its client from.
type ClientConfig struct {
	SessionKey     string
	Clearance      string
	UserAgent      string
	BrowserHeaders map[string]string
	BrowserCookies map[string]string
}

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
	// Limits is the current unified contract used by claude.ai. Older
	// responses exposed windows as top-level objects, so both shapes are
	// decoded and normalized into Windows.
	Limits []Limit `json:"limits"`
	// Windows preserves every top-level usage window under the label sent by
	// Anthropic. The named fields above remain compatibility aliases for the
	// two stable aggregate windows and the historical Opus window.
	Windows map[string]*Window `json:"-"`

	// Unrecognised names the blocks that were present but did not have
	// the shape this decoder reads. They are dropped rather than read as
	// zeros; the poller reports them.
	Unrecognised []string `json:"-"`
}

// Limit is one entry in claude.ai's current unified limits array. Kind is
// vendor-owned; Scope carries the optional model or surface label that makes
// otherwise identical kinds distinct.
type Limit struct {
	Kind     string     `json:"kind"`
	Percent  *float64   `json:"percent"`
	ResetsAt string     `json:"resets_at"`
	Scope    LimitScope `json:"scope"`
}

type LimitScope struct {
	Model   *LimitScopeValue `json:"model"`
	Surface *LimitScopeValue `json:"surface"`
}

type LimitScopeValue struct {
	DisplayName string `json:"display_name"`
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
		// seven_day_breakdown is a presentation container used by the web
		// client, not an independently metered window. Its actual scoped
		// readings arrive in limits and are decoded below.
		if name == "extra_usage" || name == "limits" || name == "seven_day_breakdown" || string(raw) == "null" {
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
	for index := range u.Limits {
		limit := &u.Limits[index]
		if limit.Kind == "" || limit.Percent == nil {
			u.Unrecognised = append(u.Unrecognised, "limits["+strconv.Itoa(index)+"]")
			continue
		}
		name := u.limitWindowName(*limit, index)
		u.Windows[name] = &Window{
			Utilization:  limit.Percent,
			ResetsAt:     limit.ResetsAt,
			Kind:         limit.Kind,
			ModelScope:   scopeDisplayName(limit.Scope.Model),
			SurfaceScope: scopeDisplayName(limit.Scope.Surface),
		}
	}
	u.syncNamedWindows()
	return nil
}

func scopeDisplayName(scope *LimitScopeValue) string {
	if scope == nil {
		return ""
	}
	return scope.DisplayName
}

func (u *UsageResponse) limitWindowName(limit Limit, index int) string {
	var base string
	switch limit.Kind {
	case "session":
		base = "five_hour"
	case "weekly_all":
		base = "seven_day"
	default:
		base = sanitizeWindowName(limit.Kind)
		if model := sanitizeWindowName(scopeDisplayName(limit.Scope.Model)); model != "" {
			base += "_" + model
		}
		if surface := sanitizeWindowName(scopeDisplayName(limit.Scope.Surface)); surface != "" {
			base += "_" + surface
		}
	}
	if base == "" {
		base = "limit"
	}
	name := base
	canonical := limit.Kind == "session" || limit.Kind == "weekly_all"
	if _, exists := u.Windows[name]; exists && !canonical {
		name = base + "_" + strconv.Itoa(index)
	}
	return name
}

func sanitizeWindowName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	underscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
			underscore = false
		case out.Len() > 0 && !underscore:
			out.WriteByte('_')
			underscore = true
		}
	}
	return strings.Trim(out.String(), "_")
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
	Utilization  *float64 `json:"utilization"`
	ResetsAt     string   `json:"resets_at"`
	Kind         string   `json:"-"`
	ModelScope   string   `json:"-"`
	SurfaceScope string   `json:"-"`
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

var ErrUnauthorized = errors.New("claude-usage-meter: claude.ai refused the session — it has expired; sign in to claude.ai in your browser, or run `tokenops vendor-usage setup claude-subscription`")

// ParseUsage reads a usage snapshot in the shape claude.ai's /usage and
// Anthropic's OAuth usage endpoint share, dropping blocks it cannot read
// rather than reading them as zeros.
func ParseUsage(body []byte) (*UsageResponse, error) {
	var u UsageResponse
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("claude-usage-meter: decode usage: %w", err)
	}
	u.dropUnreadable()
	return &u, nil
}
