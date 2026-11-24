package accounts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerGoogleDeveloper registers the reader (readers_gen.go).
func readerGoogleDeveloper() usage.Reader { return GoogleDeveloper{} }

// GoogleDeveloper reads what is left of the Google Developer Program's
// monthly Gen AI & Cloud credit that Google AI Pro and Ultra include,
// ported from CodexBar (Sources/CodexBarCore/Resources/Plugins/
// googledeveloper.ts, Providers/GoogleDeveloper, docs/googledeveloper.md):
// the benefits page at me.developers.google.com and the one batchexecute
// call it makes (rpcid Ro0AYc), with the browser's Google session. Google
// publishes no API for it.
//
// Only the current month's grant is in the answer, not older grants still
// valid on the Cloud Billing account. A browser can hold several signed-in
// Google accounts: authuser 0, 1, ... are walked, at most eight, and the
// first whose credit is applied to a billing account wins. Benefits are
// read by position, never by their localised text.
type GoogleDeveloper struct {
	BaseURL string
	HTTP    *http.Client
}

func (GoogleDeveloper) Endpoint() string               { return "google-developer" }
func (GoogleDeveloper) Provider() eventschema.Provider { return "google-developer" }
func (GoogleDeveloper) Source() string                 { return "google-developer" }

// googleDevAccounts bounds the signed-in accounts walked, as CodexBar does.
const googleDevAccounts = 8

// googleDevCredit is the benefit type of the monthly Gen AI & Cloud credit.
const googleDevCredit = 19

var (
	googleDevToken = lazyRegexp(`"SNlM0e":("(?:[^"\\]|\\.)*")`)
	googleDevEmail = lazyRegexp(`"oPEP7c":("(?:[^"\\]|\\.)*")`)
)

var errGoogleDevShape = errors.New("accounts: google developer program: unrecognised answer")

func (g GoogleDeveloper) Read(ctx context.Context, key string) (usage.Reading, error) {
	cookie := cookieHeader(key)
	if !isSession(cookie) {
		return usage.Reading{}, fmt.Errorf("%w (the Google Developer Program credential is not a Cookie header)", usage.ErrAuth)
	}
	origin := base(g.BaseURL, "https://me.developers.google.com")
	seen := map[string]bool{}
	unapplied := false
	for account := range googleDevAccounts {
		page, err := doWeb(ctx, g.HTTP, http.MethodGet, origin+"/benefits?authuser="+strconv.Itoa(account),
			http.Header{"Accept": {"text/html"}, "Cookie": {cookie}}, nil)
		if err != nil {
			if account == 0 || !errors.Is(err, usage.ErrAuth) {
				return usage.Reading{}, err
			}
			break
		}
		token, email := pageString(page, googleDevToken), pageString(page, googleDevEmail)
		if token == "" || email == "" || seen[email] {
			if account == 0 {
				return usage.Reading{}, fmt.Errorf("%w (no signed-in Google account on the benefits page)", usage.ErrAuth)
			}
			break
		}
		seen[email] = true
		freq, _ := json.Marshal([]any{[]any{[]any{"Ro0AYc", "[]", nil, "generic"}}})
		body, err := doWeb(ctx, g.HTTP, http.MethodPost,
			origin+"/_/GoogleDeveloperProfile/data/batchexecute?rpcids=Ro0AYc&source-path=%2Fbenefits&hl=en&authuser="+strconv.Itoa(account)+"&rt=c",
			http.Header{
				"Cookie":       {cookie},
				"Content-Type": {"application/x-www-form-urlencoded"},
				"Origin":       {origin},
				"Referer":      {origin + "/benefits"},
			}, formBody([][2]string{{"f.req", string(freq)}, {"at", token}}))
		if err != nil {
			if account == 0 || !errors.Is(err, usage.ErrAuth) {
				return usage.Reading{}, err
			}
			break
		}
		offer, err := googleDevOffer(body)
		switch {
		case err != nil:
			return usage.Reading{}, err
		case offer == nil:
			continue
		case len(offer) <= 6 || offer[6] == nil:
			// Not applied to a billing account yet: no money fields.
			unapplied = true
			continue
		}
		return googleDevReading(offer)
	}
	if unapplied {
		return usage.Reading{}, errors.New("accounts: google developer program: the monthly credit is not applied to a billing account yet (apply it at me.developers.google.com/benefits)")
	}
	return usage.Reading{}, errors.New("accounts: google developer program: no signed-in account has the monthly Gen AI & Cloud credit")
}

// googleDevReading reads the applied credit's money out of its offer.
func googleDevReading(offer []any) (usage.Reading, error) {
	billing, ok := offer[6].([]any)
	if !ok || len(billing) < 2 {
		return usage.Reading{}, errGoogleDevShape
	}
	granted, gc, ok1 := googleMoney(billing[0])
	remaining, rc, ok2 := googleMoney(billing[1])
	if !ok1 || !ok2 || granted <= 0 || gc != rc {
		return usage.Reading{}, errGoogleDevShape
	}
	r := usage.Reading{Scope: "account"}
	setMoney(&r, max(0, remaining), gc)
	return r, nil
}

// googleDevOffer finds the credit's offer in a batchexecute answer: nil
// when the account has no credit or its RPC failed.
func googleDevOffer(body []byte) ([]any, error) {
	var result []any
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() && result == nil {
		line := bytes.TrimSpace(sc.Bytes())
		if !bytes.HasPrefix(line, []byte("[")) {
			continue
		}
		var chunk []any
		if json.Unmarshal(line, &chunk) != nil {
			return nil, errGoogleDevShape
		}
		for _, e := range chunk {
			if entry, ok := e.([]any); ok && len(entry) > 2 && entry[0] == "wrb.fr" && entry[1] == "Ro0AYc" {
				result = entry
				break
			}
		}
	}
	if result == nil {
		return nil, errGoogleDevShape
	}
	payload, ok := result[2].(string)
	if !ok {
		return nil, nil // this account's RPC failed
	}
	var outer []any
	if json.Unmarshal([]byte(payload), &outer) != nil {
		return nil, errGoogleDevShape
	}
	if len(outer) == 0 || outer[0] == nil {
		return nil, nil
	}
	benefits, ok := outer[0].([]any)
	if !ok {
		return nil, errGoogleDevShape
	}
	for _, b := range benefits {
		benefit, ok := b.([]any)
		if !ok {
			return nil, errGoogleDevShape
		}
		if len(benefit) == 0 || benefit[0] != float64(googleDevCredit) {
			continue
		}
		if len(benefit) < 10 || benefit[9] == nil {
			return []any{}, nil
		}
		offer, ok := benefit[9].([]any)
		if !ok {
			return nil, errGoogleDevShape
		}
		return offer, nil
	}
	return nil, nil
}

// googleMoney reads a Google Money array, [currency, units, nanos], whose
// zero units and nanos are omitted.
func googleMoney(v any) (amount float64, currency string, ok bool) {
	m, isList := v.([]any)
	if !isList || len(m) == 0 {
		return 0, "", false
	}
	currency, _ = m[0].(string)
	if !currencyCode().MatchString(currency) {
		return 0, "", false
	}
	part := func(i int) (float64, bool) {
		if i >= len(m) || m[i] == nil {
			return 0, true
		}
		f, isNum := m[i].(float64)
		return f, isNum && f == float64(int64(f))
	}
	units, ok1 := part(1)
	nanos, ok2 := part(2)
	return units + nanos/1e9, currency, ok1 && ok2
}

// pageString is a JSON string literal the page's WIZ_global_data holds
// under a key, unquoted; "" when absent.
func pageString(page []byte, re func() *regexp.Regexp) string {
	m := re().FindSubmatch(page)
	if m == nil {
		return ""
	}
	var s string
	if json.Unmarshal(m[1], &s) != nil {
		return ""
	}
	return s
}
