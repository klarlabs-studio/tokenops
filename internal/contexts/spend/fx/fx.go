// Package fx converts US-dollar amounts into the operator's currency.
//
// Usage is priced in US dollars, because that is how vendors publish
// rate cards. Plans are paid in whatever the bill says. Showing one in
// euros and the other in dollars invites comparing €243 with $11,300 as
// if they were the same unit, so every total is shown in one currency,
// and every converted amount says which rate it used: a euro figure
// moves with the exchange rate even when usage does not.
package fx

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Rate converts US dollars into Currency.
type Rate struct {
	Currency string  `json:"currency"`
	PerUSD   float64 `json:"per_usd"`
	// Date is the day the rate applies to (YYYY-MM-DD), when known.
	Date string `json:"date,omitempty"`
	// Source is "ecb" for the European Central Bank's reference rate or
	// "config" for a rate the operator set.
	Source string `json:"source"`
}

// Sources.
const (
	SourceECB    = "ecb"
	SourceConfig = "config"
)

// USD is the identity rate.
var USD = Rate{Currency: "USD", PerUSD: 1, Source: SourceConfig}

// Valid reports whether the rate can convert.
func (r Rate) Valid() bool { return r.Currency != "" && r.PerUSD > 0 }

// IsUSD reports whether conversion is the identity.
func (r Rate) IsUSD() bool { return strings.EqualFold(r.Currency, "USD") }

// FromUSD converts a US-dollar amount.
func (r Rate) FromUSD(usd float64) float64 { return usd * r.PerUSD }

// Note says which rate a converted amount used, quoted the way the
// source publishes it: the ECB quotes dollars per euro.
func (r Rate) Note() string {
	if !r.Valid() || r.IsUSD() {
		return ""
	}
	switch r.Source {
	case SourceECB:
		return fmt.Sprintf("1 %s = %.4f USD (ECB reference rate, %s)", r.Currency, 1/r.PerUSD, r.Date)
	default:
		return fmt.Sprintf("1 USD = %.4f %s (your rate)", r.PerUSD, r.Currency)
	}
}

// ECB is one day's euro reference rates: how much of each currency one
// euro buys.
type ECB struct {
	Date   string             `json:"date"`
	PerEUR map[string]float64 `json:"per_eur"`
}

// ParseECB reads the ECB's daily reference-rate XML.
func ParseECB(data []byte) (ECB, error) {
	var doc struct {
		Cube struct {
			Cube struct {
				Time  string `xml:"time,attr"`
				Rates []struct {
					Currency string `xml:"currency,attr"`
					Rate     string `xml:"rate,attr"`
				} `xml:"Cube"`
			} `xml:"Cube"`
		} `xml:"Cube"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return ECB{}, fmt.Errorf("fx: parse ECB rates: %w", err)
	}
	out := ECB{Date: doc.Cube.Cube.Time, PerEUR: map[string]float64{}}
	for _, r := range doc.Cube.Cube.Rates {
		v, err := strconv.ParseFloat(r.Rate, 64)
		if err == nil && v > 0 {
			out.PerEUR[strings.ToUpper(r.Currency)] = v
		}
	}
	if out.Date == "" || out.PerEUR["USD"] <= 0 {
		return ECB{}, errors.New("fx: ECB rates carry no date or no USD rate")
	}
	return out, nil
}

// Rate is the conversion from US dollars into currency, through the
// euro: one dollar buys 1/USD euros, and each euro buys PerEUR[currency].
func (e ECB) Rate(currency string) (Rate, bool) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	usd := e.PerEUR["USD"]
	if usd <= 0 {
		return Rate{}, false
	}
	perEUR := 1.0
	if currency != "EUR" {
		perEUR = e.PerEUR[currency]
	}
	if perEUR <= 0 {
		return Rate{}, false
	}
	return Rate{Currency: currency, PerUSD: perEUR / usd, Date: e.Date, Source: SourceECB}, true
}

// regionCurrency maps an ISO 3166 region to the currency its residents
// are billed in.
var regionCurrency = map[string]string{
	"US": "USD", "GB": "GBP", "CH": "CHF", "LI": "CHF", "CA": "CAD", "AU": "AUD", "NZ": "NZD",
	"JP": "JPY", "IN": "INR", "SE": "SEK", "NO": "NOK", "DK": "DKK", "PL": "PLN", "CZ": "CZK",
	"HU": "HUF", "RO": "RON", "BR": "BRL", "MX": "MXN", "SG": "SGD", "HK": "HKD", "KR": "KRW",
	"ZA": "ZAR", "IL": "ILS", "TR": "TRY", "CN": "CNY",
	"DE": "EUR", "AT": "EUR", "FR": "EUR", "IT": "EUR", "ES": "EUR", "NL": "EUR", "BE": "EUR",
	"IE": "EUR", "PT": "EUR", "FI": "EUR", "GR": "EUR", "LU": "EUR", "SK": "EUR", "SI": "EUR",
	"EE": "EUR", "LV": "EUR", "LT": "EUR", "MT": "EUR", "CY": "EUR", "HR": "EUR",
}

// CurrencyForRegion is the currency of an ISO 3166 region code.
func CurrencyForRegion(region string) (string, bool) {
	c, ok := regionCurrency[strings.ToUpper(strings.TrimSpace(region))]
	return c, ok
}

// RegionFromLocale reads the region from a locale identifier. A macOS
// region override ("en_US@rg=dezzzz") wins over the language's region,
// because it is the setting that says where the person lives: an
// English-language system in Germany is billed in euros.
func RegionFromLocale(locale string) string {
	if i := strings.Index(locale, "@rg="); i >= 0 {
		rg := locale[i+4:]
		if len(rg) >= 2 {
			return strings.ToUpper(rg[:2])
		}
	}
	locale = strings.SplitN(locale, ".", 2)[0]
	locale = strings.SplitN(locale, "@", 2)[0]
	if i := strings.IndexAny(locale, "_-"); i >= 0 && len(locale) >= i+3 {
		return strings.ToUpper(locale[i+1 : i+3])
	}
	return ""
}
