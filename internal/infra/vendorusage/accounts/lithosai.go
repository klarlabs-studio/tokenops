package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerLithosAI registers the reader (readers_gen.go).
func readerLithosAI() usage.Reader { return LithosAI{} }

// LithosAI reads the active organisation's prepaid balance, and its spend
// this UTC month, from the LithosAI console with its browser session, as
// CodexBar's LithosAI provider does
// (Sources/CodexBarCore/Resources/Plugins/lithosai.ts, docs/lithosai.md).
// Amounts are in nano-dollars. The month's spend is optional: when it
// cannot be read, the balance alone is reported.
type LithosAI struct {
	BaseURL string
	HTTP    *http.Client
	Now     func() time.Time
}

func (LithosAI) Endpoint() string               { return "lithosai" }
func (LithosAI) Provider() eventschema.Provider { return "lithosai" }
func (LithosAI) Source() string                 { return "lithosai-web" }

const (
	lithosSession = "__Host-console_session"
	lithosCSRF    = "__Host-console_csrf"
	// lithosMaxSafe is the largest integer JavaScript (and so the console)
	// holds exactly.
	lithosMaxSafe = 1<<53 - 1
)

var (
	lithosOrgID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	lithosDay   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

func (l LithosAI) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	cookie = cookieHeader(cookie)
	csrf := cookieValue(cookie, lithosCSRF)
	if !isSession(cookie) || cookieValue(cookie, lithosSession) == "" || csrf == "" || strings.ContainsAny(csrf, "\r\n") {
		return usage.Reading{}, fmt.Errorf("%w (the LithosAI console is read with its session and CSRF cookies)", usage.ErrAuth)
	}
	root := base(l.BaseURL, "https://console.lithosai.cloud")
	header := http.Header{"Cookie": {cookie}, "X-Console-Csrf": {csrf}}
	var me struct {
		Org *struct {
			ID string `json:"id"`
		} `json:"activeOrganization"`
	}
	if err := webJSON(ctx, l.HTTP, root+"/api/me", header, &me); err != nil {
		return usage.Reading{}, err
	}
	if me.Org == nil || !lithosOrgID.MatchString(strings.TrimSpace(me.Org.ID)) {
		return usage.Reading{}, errors.New("accounts: GET console.lithosai.cloud/api/me: no active organisation")
	}
	header = header.Clone()
	header.Set("X-Organization-Id", strings.TrimSpace(me.Org.ID))
	var billing struct {
		BalanceNanos json.RawMessage `json:"balanceNanos"`
	}
	if err := webJSON(ctx, l.HTTP, root+"/api/billing", header, &billing); err != nil {
		return usage.Reading{}, err
	}
	nanos, ok := lithosNanos(billing.BalanceNanos, true)
	if !ok {
		return usage.Reading{}, errors.New("accounts: GET console.lithosai.cloud/api/billing: unreadable balance")
	}
	r := usage.Reading{Scope: "account", BalanceUSD: nanos / 1e9, HasBalance: true}
	spent, err := l.monthSpend(ctx, root, header)
	if errors.Is(err, usage.ErrAuth) {
		return usage.Reading{}, err
	}
	if err == nil {
		r.UsedUSD, r.HasUsed = spent/1e9, true
	}
	return r, nil
}

// monthSpend sums the month's daily spend rows (per model and key), in
// nano-dollars; the answer must echo the asked range.
func (l LithosAI) monthSpend(ctx context.Context, root string, header http.Header) (float64, error) {
	now := time.Now
	if l.Now != nil {
		now = l.Now
	}
	today := now().UTC()
	end := today.Format("2006-01-02")
	start := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	var spend struct {
		Start string `json:"start"`
		End   string `json:"end"`
		Days  *[]struct {
			Day   string          `json:"day"`
			Nanos json.RawMessage `json:"nanos"`
		} `json:"days"`
	}
	if err := webJSON(ctx, l.HTTP, root+"/api/billing/spend?start="+start+"&end="+end, header, &spend); err != nil {
		return 0, err
	}
	if spend.Start != start || spend.End != end || spend.Days == nil {
		return 0, errors.New("accounts: lithosai spend: another range")
	}
	total := 0.0
	for _, d := range *spend.Days {
		n, ok := lithosNanos(d.Nanos, false)
		if !ok || !lithosDay.MatchString(d.Day) || d.Day < start || d.Day > end {
			return 0, errors.New("accounts: lithosai spend: unreadable row")
		}
		total += n
	}
	return total, nil
}

// lithosNanos is a whole JSON number within JavaScript's safe range,
// below zero only when signed.
func lithosNanos(raw json.RawMessage, signed bool) (float64, bool) {
	v, ok := finiteNumber(raw)
	if !ok || v != math.Trunc(v) || math.Abs(v) > lithosMaxSafe || (!signed && v < 0) {
		return 0, false
	}
	return v, true
}

// webJSON GETs a console route with a session and decodes the answer.
func webJSON(ctx context.Context, hc *http.Client, u string, header http.Header, out any) error {
	body, err := doWeb(ctx, hc, http.MethodGet, u, header, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("accounts: GET %s: %w", hostPath(u), err)
	}
	return nil
}
