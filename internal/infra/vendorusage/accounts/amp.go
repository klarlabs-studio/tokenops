package accounts

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Amp is read two ways, after CodexBar: the operator's own Amp CLI (`amp
// usage`, which signs its own request), then Amp's internal balance
// endpoint with an access token (AMP_API_KEY, or one typed into setup).
// Both answer with the same display text the CLI prints, parsed alike.

// readerAmpCLI registers the CLI reader (readers_gen.go).
func readerAmpCLI() usage.Reader { return AmpCLI{} }

// AmpCLI runs `amp usage`.
type AmpCLI struct {
	// Bin overrides finding amp (AMP_CLI_PATH, PATH, install
	// directories); Now the clock. For tests.
	Bin string
	Now func() time.Time
}

func (AmpCLI) Endpoint() string               { return "amp" }
func (AmpCLI) Provider() eventschema.Provider { return "amp" }
func (AmpCLI) Source() string                 { return "amp-cli" }
func (AmpCLI) Keyless()                       {}

func (a AmpCLI) Read(ctx context.Context, _ string) (usage.Reading, error) {
	bin, ok := a.Bin, a.Bin != ""
	if !ok {
		bin, ok = locateCLI("amp", strings.TrimSpace(os.Getenv("AMP_CLI_PATH")))
	}
	if !ok {
		return usage.Reading{}, usage.ErrNotInstalled
	}
	out, err := runCLI(ctx, 15*time.Second, bin, nil, "usage")
	if err != nil {
		if errors.Is(err, errCLIFailed) && ampSignedOut(out) {
			return usage.Reading{}, cliAuthError("amp")
		}
		return usage.Reading{}, err
	}
	return parseAmpDisplay(out, clock(a.Now))
}

// readerAmp registers the access-token reader (readers_gen.go).
func readerAmp() usage.Reader { return Amp{} }

// Amp reads POST /api/internal?userDisplayBalanceInfo, the call Amp's own
// CLI makes, with an Amp access token. It is not published.
type Amp struct {
	BaseURL string
	HTTP    *http.Client
	Now     func() time.Time
}

func (Amp) Endpoint() string               { return "amp" }
func (Amp) Provider() eventschema.Provider { return "amp" }
func (Amp) Source() string                 { return "amp-account" }

func (a Amp) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			DisplayText string `json:"displayText"`
		} `json:"result"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	url := base(a.BaseURL, "https://ampcode.com") + "/api/internal?userDisplayBalanceInfo"
	err := sendJSON(ctx, a.HTTP, http.MethodPost, url, map[string]string{
		"Authorization": "Bearer " + key, "Accept": "application/json", "Content-Type": "application/json",
	}, []byte(`{"method":"userDisplayBalanceInfo","params":{}}`), &resp)
	if err != nil {
		return usage.Reading{}, err
	}
	switch {
	case !resp.OK && resp.Error.Code == "auth-required":
		return usage.Reading{}, fmt.Errorf("%w (Amp: auth-required)", usage.ErrAuth)
	case !resp.OK:
		return usage.Reading{}, fmt.Errorf("accounts: Amp answered with an error")
	case strings.TrimSpace(resp.Result.DisplayText) == "":
		return usage.Reading{}, fmt.Errorf("accounts: Amp returned no usage")
	}
	return parseAmpDisplay(resp.Result.DisplayText, clock(a.Now))
}

func clock(now func() time.Time) time.Time {
	if now != nil {
		return now()
	}
	return time.Now()
}

const ampNum = `([0-9][0-9,]*(?:\.[0-9]+)?)`

var (
	ampIdentity    = regexp.MustCompile(`(?im)^\s*Signed in as\s+([^\s(]+)`)
	ampFreeDollars = regexp.MustCompile(`(?im)^\s*Amp Free:\s*\$?` + ampNum + `\s*/\s*\$?` + ampNum + `\s+remaining(?:\s*\(replenishes\s*\+\$?` + ampNum + `\s*/\s*hour\))?`)
	ampFreePercent = regexp.MustCompile(`(?im)^\s*Amp Free:\s*` + ampNum + `\s*%\s+remaining(?:\s+today)?(?:\s*(\(resets\s+daily\)))?`)
	ampTier        = regexp.MustCompile(`(?im)^\s*Amp\s+([^\r\n]+?)\s+Tier:\s*agent\s+usage\s+\$` + ampNum + `\s+of\s+\$` + ampNum + `\s+remaining\b([^\r\n]*?)resets\s+upon\s+renewal\s+in\s+([0-9][0-9,]*)\s+(days?|months?)\b`)
	ampPeriod      = regexp.MustCompile(`\bperiod\s+(\d{4}-\d{2}-\d{2})\s+to\s+(\d{4}-\d{2}-\d{2})\b`)
	ampOrb         = regexp.MustCompile(`(?i)\borb\s+usage\s+` + ampNum + `h\s+of\s+` + ampNum + `h\s+a1\.small\s+orb\s+hours\s+remaining\b`)
	ampLegacySub   = regexp.MustCompile(`(?im)^\s*(?:Subscription\s+(?:.+?):|Amp\s+(?:.+?)\s+Subscription:)\s*` + ampNum + `\s*%\s+other\s+usage\s+and\s+` + ampNum + `\s*%\s+orb\s+usage\s+remaining\s*-\s*resets\s+upon\s+renewal\s+in\s+([0-9][0-9,]*)\s+(days?|months?)(?:\s+-\s+https?://\S+)?\s*$`)
	ampIndividual  = regexp.MustCompile(`(?im)^\s*Individual credits:\s*\$?` + ampNum + `\s+remaining`)
	ampWorkspace   = regexp.MustCompile(`(?im)^\s*Workspace\s+(.+?):\s*\$?` + ampNum + `\s+remaining`)
)

func ampSignedOut(text string) bool {
	return !ampIdentity.MatchString(text) && mentions(text, "sign in", "log in", "login")
}

func ampFloat(s string) float64 {
	v, _ := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	return v
}

// parseAmpDisplay maps Amp's usage text: the subscription's agent usage
// and orb hours for the billing period, Amp Free's daily allowance, and
// the individual credit balance. Workspace balances are not read: a
// reading has one balance.
func parseAmpDisplay(text string, now time.Time) (usage.Reading, error) {
	text = strings.ReplaceAll(stripANSI(text), "**", "")
	if ampSignedOut(text) {
		return usage.Reading{}, cliAuthError("amp")
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	if m := ampTier.FindStringSubmatch(text); m != nil && ampFloat(m[3]) > 0 {
		limit := ampFloat(m[3])
		w := usage.Window{Name: "month", UsedPct: clampPct((limit - ampFloat(m[2])) / limit * 100)}
		if start, end, ok := ampBillingPeriod(m[4]); ok {
			w.ResetsAt, w.Duration = end, end.Sub(start)
		} else {
			w.ResetsAt = ampRenewal(now, m[5], m[6])
		}
		r.Windows = append(r.Windows, w)
		if o := ampOrb.FindStringSubmatch(m[4]); o != nil && ampFloat(o[2]) > 0 {
			orbLimit := ampFloat(o[2])
			r.Windows = append(r.Windows, usage.Window{Name: "month (orb hours)", UsedPct: clampPct((orbLimit - ampFloat(o[1])) / orbLimit * 100),
				Duration: w.Duration, ResetsAt: w.ResetsAt})
		}
	} else if m := ampLegacySub.FindStringSubmatch(text); m != nil {
		resets := ampRenewal(now, m[3], m[4])
		r.Windows = append(r.Windows,
			usage.Window{Name: "month", UsedPct: 100 - clampPct(ampFloat(m[1])), ResetsAt: resets},
			usage.Window{Name: "month (orb hours)", UsedPct: 100 - clampPct(ampFloat(m[2])), ResetsAt: resets})
	}
	if w, ok := ampFree(text, now); ok {
		r.Windows = append(r.Windows, w)
	}
	if m := ampIndividual.FindStringSubmatch(text); m != nil {
		r.BalanceUSD, r.HasBalance = ampFloat(m[1]), true
	}
	if len(r.Windows) == 0 && !r.HasBalance && !ampWorkspace.MatchString(text) {
		return usage.Reading{}, fmt.Errorf("accounts: Amp usage in a shape this version cannot read")
	}
	return r, nil
}

// ampFree is Amp Free's allowance: a share left today, reset at 8 pm New
// York time, or (older output) dollars left of a quota that replenishes
// hourly, full again after used/hourly hours.
func ampFree(text string, now time.Time) (usage.Window, bool) {
	if m := ampFreeDollars.FindStringSubmatch(text); m != nil && ampFloat(m[2]) > 0 {
		quota := ampFloat(m[2])
		used := math.Max(0, quota-ampFloat(m[1]))
		w := usage.Window{Name: "day", UsedPct: clampPct(used / quota * 100)}
		if hourly := ampFloat(m[3]); hourly > 0 {
			hours := math.Max(1, math.Round(quota/hourly))
			w.Duration = time.Duration(hours) * time.Hour
			w.Name = windowName(w.Duration)
			w.ResetsAt = now.Add(time.Duration(used / hourly * float64(time.Hour))).UTC()
		}
		return w, true
	}
	if m := ampFreePercent.FindStringSubmatch(text); m != nil {
		w := usage.Window{Name: "day", UsedPct: 100 - clampPct(ampFloat(m[1])), Duration: 24 * time.Hour}
		if m[2] != "" {
			w.ResetsAt = ampNextDailyReset(now)
		}
		return w, true
	}
	return usage.Window{}, false
}

// ampNextDailyReset is the next 8 pm in New York after now.
func ampNextDailyReset(now time.Time) time.Time {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.Time{}
	}
	t := now.In(ny)
	reset := time.Date(t.Year(), t.Month(), t.Day(), 20, 0, 0, 0, ny)
	if !reset.After(t) {
		reset = time.Date(t.Year(), t.Month(), t.Day()+1, 20, 0, 0, 0, ny)
	}
	return reset.UTC()
}

// ampBillingPeriod reads "period 2026-09-13 to 2026-10-13" as UTC days.
func ampBillingPeriod(s string) (time.Time, time.Time, bool) {
	m := ampPeriod.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, time.Time{}, false
	}
	start, err1 := time.Parse("2006-01-02", m[1])
	end, err2 := time.Parse("2006-01-02", m[2])
	if err1 != nil || err2 != nil || !end.After(start) {
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}

// ampRenewal is now plus "27 days" or "1 month".
func ampRenewal(now time.Time, count, unit string) time.Time {
	n, err := strconv.Atoi(strings.ReplaceAll(count, ",", ""))
	if err != nil || n < 0 || n > 1200 {
		return time.Time{}
	}
	if strings.HasPrefix(strings.ToLower(unit), "month") {
		return now.AddDate(0, n, 0).UTC()
	}
	return now.Add(time.Duration(n) * 24 * time.Hour).UTC()
}
