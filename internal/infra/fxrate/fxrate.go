// Package fxrate resolves the rate TokenOps converts US dollars with.
//
// A rate the operator pinned in config wins. Otherwise it is the ECB's
// daily euro reference rate, fetched at most once a day and cached under
// ~/.tokenops. The fetch is a GET of a public file and sends nothing:
// no identifier, no usage, no amount. When it fails, the last cached
// rate is used and its date says how old it is.
package fxrate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/fx"
)

// ECBURL is the ECB's daily reference-rate file. A variable so tests can
// point it at a local server.
var ECBURL = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"

// maxAge is how long a cached rate is used before refetching. The ECB
// publishes once per working day.
const maxAge = 24 * time.Hour

type cache struct {
	Fetched time.Time `json:"fetched"`
	fx.ECB
}

// cachePath is where the last ECB rates are kept.
func cachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tokenops", "fx-ecb.json"), nil
}

// Resolve returns the rate for the configured currency. ok is false when
// no rate is available; warning explains a degraded answer (a stale
// cache, a failed fetch) and is empty otherwise.
func Resolve(ctx context.Context, m config.MoneyConfig, now time.Time) (rate fx.Rate, ok bool, warning string) {
	currency := strings.ToUpper(strings.TrimSpace(m.Currency))
	if currency == "" || currency == "USD" {
		return fx.USD, true, ""
	}
	if m.PerUSD > 0 {
		return fx.Rate{Currency: currency, PerUSD: m.PerUSD, Source: fx.SourceConfig}, true, ""
	}
	path, err := cachePath()
	if err != nil {
		return fx.Rate{}, false, err.Error()
	}
	cached, haveCache := readCache(path)
	if haveCache && now.Sub(cached.Fetched) < maxAge {
		r, ok := cached.Rate(currency)
		return r, ok, unpublished(ok, currency)
	}
	if !m.FetchesRate() {
		if haveCache {
			r, ok := cached.Rate(currency)
			return r, ok, unpublished(ok, currency)
		}
		return fx.Rate{}, false, "money.fetch_rate is off and money.per_usd is not set, so dollar amounts are not converted"
	}
	fresh, err := fetch(ctx)
	if err != nil {
		if haveCache {
			r, ok := cached.Rate(currency)
			return r, ok, fmt.Sprintf("could not refresh the ECB rate (%v); using the one from %s", err, cached.Date)
		}
		return fx.Rate{}, false, fmt.Sprintf("could not fetch the ECB rate: %v", err)
	}
	writeCache(path, cache{Fetched: now, ECB: fresh})
	r, ok := fresh.Rate(currency)
	return r, ok, unpublished(ok, currency)
}

func unpublished(ok bool, currency string) string {
	if ok {
		return ""
	}
	return fmt.Sprintf("the ECB publishes no rate for %s; set money.per_usd", currency)
}

func readCache(path string) (cache, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return cache{}, false
	}
	var c cache
	if json.Unmarshal(data, &c) != nil || c.PerEUR["USD"] <= 0 {
		return cache{}, false
	}
	return c, true
}

func writeCache(path string, c cache) {
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

func fetch(ctx context.Context) (fx.ECB, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ECBURL, nil)
	if err != nil {
		return fx.ECB{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fx.ECB{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fx.ECB{}, fmt.Errorf("ECB answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fx.ECB{}, err
	}
	return fx.ParseECB(data)
}

// DetectCurrency guesses the operator's billing currency from the
// system's region, and says where it looked. On macOS the region setting
// ("en_US@rg=dezzzz") wins over LANG, which often names only the
// language: an English-language system in Germany is billed in euros.
func DetectCurrency() (currency, from string) {
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("defaults", "read", "-g", "AppleLocale").Output(); err == nil {
			if c, ok := fx.CurrencyForRegion(fx.RegionFromLocale(strings.TrimSpace(string(out)))); ok {
				return c, "your macOS region"
			}
		}
	}
	for _, k := range []string{"LC_MONETARY", "LC_ALL", "LANG"} {
		if c, ok := fx.CurrencyForRegion(fx.RegionFromLocale(os.Getenv(k))); ok {
			return c, k
		}
	}
	return "USD", "no region found"
}
