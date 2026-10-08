package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Augment Code is read two ways, after CodexBar: the operator's own
// Auggie CLI (`auggie account status`, which signs its own request), then
// app.augmentcode.com's credits endpoint with the browser session, which
// only `tokenops vendor-usage setup augment` reads from the browser.

// readerAugmentCLI registers the CLI reader (readers_gen.go).
func readerAugmentCLI() usage.Reader { return AugmentCLI{} }

// AugmentCLI runs `auggie account status`.
type AugmentCLI struct {
	// Bin overrides finding auggie (AUGGIE_CLI_PATH, PATH, install
	// directories), for tests.
	Bin string
}

func (AugmentCLI) Endpoint() string               { return "augment" }
func (AugmentCLI) Provider() eventschema.Provider { return "augment" }
func (AugmentCLI) Source() string                 { return "augment-cli" }
func (AugmentCLI) Keyless()                       {}

func (a AugmentCLI) Read(ctx context.Context, _ string) (usage.Reading, error) {
	bin, ok := a.Bin, a.Bin != ""
	if !ok {
		bin, ok = locateCLI("auggie", strings.TrimSpace(os.Getenv("AUGGIE_CLI_PATH")))
	}
	if !ok {
		return usage.Reading{}, usage.ErrNotInstalled
	}
	out, err := runCLI(ctx, 15*time.Second, bin, nil, "account", "status")
	if strings.Contains(out, "Authentication failed") || strings.Contains(out, "auggie login") {
		return usage.Reading{}, cliAuthError("auggie")
	}
	if err != nil {
		return usage.Reading{}, err
	}
	return parseAuggieStatus(out)
}

var (
	auggieMonthly    = regexp.MustCompile(`([\d,]+)\s+credits\s*/\s*month`)
	auggieMaxPlan    = regexp.MustCompile(`([\d,]+)\s+credits`)
	auggieRemaining  = regexp.MustCompile(`([\d,]+)\s+credits\s+remaining`)
	auggieLegacyLeft = regexp.MustCompile(`([\d,]+)\s+remaining`)
	auggieLegacyUsed = regexp.MustCompile(`([\d,]+)\s*/\s*([\d,]+)\s+credits used`)
	auggieCycleEnd   = regexp.MustCompile(`ends\s+([\d/]+)`)
)

// parseAuggieStatus maps the account box: credits remaining of the
// month's allowance, the billing cycle ending on the date printed.
func parseAuggieStatus(text string) (usage.Reading, error) {
	var (
		remaining, used, total, monthly *float64
		ends                            time.Time
	)
	val := func(s string) *float64 { v := ampFloat(s); return &v }
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.Contains(line, "credits / month"):
			if m := auggieMonthly.FindStringSubmatch(line); m != nil {
				monthly = val(m[1])
			}
		case strings.Contains(line, "Max Plan") && strings.Contains(line, "credits") && !strings.Contains(line, "remaining"):
			if m := auggieMaxPlan.FindStringSubmatch(line); m != nil {
				monthly = val(m[1])
			}
		}
		if strings.Contains(line, "credits remaining") && !strings.Contains(line, "billing cycle") {
			if m := auggieRemaining.FindStringSubmatch(line); m != nil {
				remaining = val(m[1])
			}
		}
		if strings.Contains(line, "remaining") && strings.Contains(line, "credits used") {
			if m := auggieLegacyLeft.FindStringSubmatch(line); m != nil {
				remaining = val(m[1])
			}
			if m := auggieLegacyUsed.FindStringSubmatch(line); m != nil {
				used, total = val(m[1]), val(m[2])
			}
		}
		if strings.Contains(line, "billing cycle") && strings.Contains(line, "ends") {
			if m := auggieCycleEnd.FindStringSubmatch(line); m != nil {
				if t, err := time.ParseInLocation("1/2/2006", m[1], time.Local); err == nil {
					ends = t.UTC()
				}
			}
		}
	}
	if total == nil {
		total = monthly
	}
	if remaining == nil || total == nil || *total <= 0 {
		return usage.Reading{}, fmt.Errorf("accounts: auggie account status in a shape this version cannot read")
	}
	u := *total - *remaining
	if used != nil {
		u = *used
	}
	return augmentReading(u, *total, ends), nil
}

func augmentReading(used, limit float64, ends time.Time) usage.Reading {
	return usage.Reading{Scope: "account", Subscription: true, Windows: []usage.Window{{
		Name: "month", UsedPct: clampPct(pct(max(used, 0), limit)), ResetsAt: ends,
	}}}
}

// readerAugment registers the web reader (readers_gen.go).
func readerAugment() usage.Reader { return Augment{} }

// Augment reads GET /api/credits (and /api/subscription for the cycle's
// end) on app.augmentcode.com, the calls its account page makes, with the
// browser session's cookies. Neither is published.
type Augment struct {
	BaseURL string
	HTTP    *http.Client
}

func (Augment) Endpoint() string               { return "augment" }
func (Augment) Provider() eventschema.Provider { return "augment" }
func (Augment) Source() string                 { return "augment-web" }

func (a Augment) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	root := base(a.BaseURL, "https://app.augmentcode.com")
	headers := map[string]string{"Cookie": cookie, "Accept": "application/json"}
	var credits struct {
		Remaining *float64 `json:"usageUnitsRemaining"`
		Consumed  *float64 `json:"usageUnitsConsumedThisBillingCycle"`
		Available *float64 `json:"usageUnitsAvailable"`
	}
	if err := sendJSON(ctx, a.HTTP, http.MethodGet, root+"/api/credits", headers, nil, &credits); err != nil {
		return usage.Reading{}, err
	}
	if credits.Remaining == nil && credits.Consumed == nil {
		return usage.Reading{}, fmt.Errorf("accounts: Augment credits in a shape this version cannot read")
	}
	remaining, consumed := deref(credits.Remaining), deref(credits.Consumed)
	limit := deref(credits.Available)
	if limit <= 0 {
		limit = remaining + consumed
	}
	if limit <= 0 {
		return usage.Reading{Scope: "account", Subscription: true}, nil
	}
	if credits.Consumed == nil {
		consumed = limit - remaining
	}
	var sub struct {
		BillingPeriodEnd string `json:"billingPeriodEnd"`
	}
	// The cycle's end is a nicety: the credits stand without it.
	if err := sendJSON(ctx, a.HTTP, http.MethodGet, root+"/api/subscription", headers, nil, &sub); err != nil && errors.Is(err, usage.ErrAuth) {
		return usage.Reading{}, err
	}
	return augmentReading(consumed, limit, parseTime(sub.BillingPeriodEnd)), nil
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
