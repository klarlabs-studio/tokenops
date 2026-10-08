package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerSakana registers the reader (readers_gen.go).
func readerSakana() usage.Reader { return Sakana{} }

// Sakana reads Sakana AI's 5-hour and weekly quota windows from the
// console's billing page, which it renders on the server and offers no
// JSON API for, and the pay-as-you-go credit balance from its
// pay-as-you-go tab, with a pasted Cookie header, as CodexBar's Sakana
// plugin does (Sources/CodexBarCore/Resources/Plugins/sakana.js). Reset
// times on the page are UTC.
type Sakana struct {
	BaseURL string
	HTTP    *http.Client
}

func (Sakana) Endpoint() string               { return "sakana" }
func (Sakana) Provider() eventschema.Provider { return "sakana" }
func (Sakana) Source() string                 { return "sakana-web" }

var (
	sakanaBoundary = regexp.MustCompile(`(?i)<p[^>]*>\s*(?:5-hour|Weekly)\s*</p>|<div[^>]*data-slot=(?:"card"|'card'|"card-title"|'card-title')[^>]*>`)
	sakanaUsed     = regexp.MustCompile(`(?i)<p[^>]*>\s*([0-9]+(?:\.[0-9]+)?)% used\s*</p>`)
	sakanaResets   = regexp.MustCompile(`(?i)<p[^>]*>\s*Resets on ([^<]+?)\s*</p>`)
	sakanaBalance  = regexp.MustCompile(`(?i)<h2[^>]*>\s*Credit balance\s*</h2>[\s\S]{0,900}?<p[^>]*tabular-nums[^"]*"[^>]*>\$?([0-9][0-9,]*(?:\.[0-9]+)?)</p>`)
)

func (s Sakana) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	if !isSession(cookie) {
		return usage.Reading{}, fmt.Errorf("%w (Sakana is read with the console's Cookie header)", usage.ErrAuth)
	}
	page := base(s.BaseURL, "https://console.sakana.ai") + "/billing"
	header := http.Header{"Cookie": {cookie}, "Accept": {"text/html,application/xhtml+xml"}, "Accept-Language": {"en-US,en;q=0.9"}}
	html, err := doWeb(ctx, s.HTTP, http.MethodGet, page, header, nil)
	if err != nil {
		return usage.Reading{}, err
	}
	if len(html) == 0 {
		return usage.Reading{}, errors.New("accounts: sakana: the billing page was empty")
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	for _, w := range []struct {
		label, name string
		d           time.Duration
	}{{"5-hour", "5h", 5 * time.Hour}, {"Weekly", "week", 7 * 24 * time.Hour}} {
		got, ok, err := sakanaWindow(string(html), w.label)
		if err != nil {
			return usage.Reading{}, err
		}
		if ok {
			got.Name, got.Duration = w.name, w.d
			r.Windows = append(r.Windows, got)
		}
	}
	if len(r.Windows) == 0 {
		if looksLikeSignIn(html) {
			return usage.Reading{}, fmt.Errorf("%w (the billing page asked to sign in)", usage.ErrAuth)
		}
		return usage.Reading{}, errors.New("accounts: sakana: no usage windows on the billing page")
	}
	// The pay-as-you-go balance is best-effort: its failure keeps the windows.
	if payg, err := doWeb(ctx, s.HTTP, http.MethodGet, page+"?tab=payAsYouGo", header, nil); err == nil {
		if m := sakanaBalance.FindSubmatch(payg); m != nil {
			if v, err := strconv.ParseFloat(strings.ReplaceAll(string(m[1]), ",", ""), 64); err == nil {
				r.BalanceUSD, r.HasBalance = v, true
			}
		}
	}
	return r, nil
}

// sakanaWindow reads one quota block: the share used and when it resets.
func sakanaWindow(html, label string) (usage.Window, bool, error) {
	head := regexp.MustCompile(`(?i)<p[^>]*>\s*` + regexp.QuoteMeta(label) + `\s*</p>`).FindStringIndex(html)
	if head == nil {
		return usage.Window{}, false, nil
	}
	rest := html[head[1]:]
	if b := sakanaBoundary.FindStringIndex(rest); b != nil {
		rest = rest[:b[0]]
	}
	if strings.TrimSpace(rest) == "" {
		return usage.Window{}, false, nil
	}
	m := sakanaUsed.FindStringSubmatch(rest)
	if m == nil {
		return usage.Window{}, false, fmt.Errorf("accounts: sakana: no %s usage percentage", label)
	}
	used, err := strconv.ParseFloat(m[1], 64)
	if err != nil || used < 0 || used > 100 {
		return usage.Window{}, false, fmt.Errorf("accounts: sakana: invalid %s usage percentage", label)
	}
	w := usage.Window{UsedPct: used}
	if rm := sakanaResets.FindStringSubmatch(rest); rm != nil {
		if t, err := time.Parse("January 2, 2006 at 3:04 PM", strings.TrimSpace(rm[1])); err == nil {
			w.ResetsAt = t.UTC()
		}
	}
	return w, true, nil
}
