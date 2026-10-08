package accounts

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerGrok and readerGrokWeb register the readers (readers_gen.go).
func readerGrok() usage.Reader    { return Grok{} }
func readerGrokWeb() usage.Reader { return GrokWeb{} }

// Grok reads a SuperGrok plan's credit window and prepaid balance with
// the Grok CLI's sign-in token, from the CLI proxy's billing call
// (cli-chat-proxy.grok.com/v1/billing?format=credits), as CodexBar's Grok
// provider does
// (Sources/CodexBarCore/Providers/Grok/GrokCreditsProxyFetcher.swift,
// GrokSettingsReader.swift, docs/grok.md). The token is GROK_OAUTH_TOKEN
// or one given in setup; xAI management keys (xai-…) are not it.
type Grok struct {
	BaseURL string
	HTTP    *http.Client
	Now     func() time.Time
}

func (Grok) Endpoint() string               { return "grok" }
func (Grok) Provider() eventschema.Provider { return "grok" }
func (Grok) Source() string                 { return "grok-account" }
func (Grok) KeyOnly() bool                  { return true }

func (g Grok) Read(ctx context.Context, key string) (usage.Reading, error) {
	if isSession(key) {
		return usage.Reading{}, usage.ErrSkip
	}
	token := strings.TrimSpace(key)
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = strings.TrimSpace(token[len("bearer "):])
	}
	if token == "" || strings.HasPrefix(token, "xai-") || strings.ContainsAny(token, " \r\n") {
		return usage.Reading{}, fmt.Errorf("%w (Grok is read with the Grok CLI's sign-in token, not an xAI key)", usage.ErrAuth)
	}
	header := http.Header{"Authorization": {"Bearer " + token}, "X-Xai-Token-Auth": {"xai-grok-cli"}, "User-Agent": {"TokenOps"}}
	var resp struct {
		Config *struct {
			Percent *float64 `json:"creditUsagePercent"`
			Current *struct {
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"currentPeriod"`
			BillingStart string          `json:"billingPeriodStart"`
			BillingEnd   string          `json:"billingPeriodEnd"`
			Cap          *grokCents      `json:"onDemandCap"`
			Used         *grokCents      `json:"onDemandUsed"`
			Prepaid      json.RawMessage `json:"prepaidBalance"`
		} `json:"config"`
	}
	u := base(g.BaseURL, "https://cli-chat-proxy.grok.com") + "/v1/billing?format=credits"
	if err := doJSON(ctx, g.HTTP, http.MethodGet, u, header, nil, &resp); err != nil {
		return usage.Reading{}, err
	}
	c := resp.Config
	if c == nil {
		return usage.Reading{}, errors.New("accounts: GET cli-chat-proxy.grok.com/v1/billing: no config")
	}
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}
	var start, end time.Time
	if c.Current != nil {
		end = parseTime(c.Current.End)
	}
	if !end.IsZero() {
		start = parseTime(c.Current.Start)
	} else {
		start, end = parseTime(c.BillingStart), parseTime(c.BillingEnd)
	}
	r := usage.Reading{Scope: "account"}
	used, hasPct := 0.0, false
	switch {
	case c.Percent != nil && !math.IsNaN(*c.Percent) && !math.IsInf(*c.Percent, 0):
		used, hasPct = *c.Percent, true
	case c.Cap != nil && c.Used != nil && c.Cap.v > 0:
		used, hasPct = pct(c.Used.v, c.Cap.v), true
	}
	if hasPct {
		w := usage.Window{UsedPct: clampPct(used), ResetsAt: end}
		if !start.IsZero() && !start.After(now()) && end.After(start) {
			w.Duration = end.Sub(start).Truncate(time.Minute)
		}
		w.Name = grokWindowName(w.Duration, end, now())
		r.Subscription, r.Windows = true, []usage.Window{w}
	}
	if v, ok := grokPrepaid(c.Prepaid); ok {
		r.BalanceUSD, r.HasBalance = v, true
	}
	if !hasPct && end.IsZero() && !r.HasBalance {
		return usage.Reading{}, errors.New("accounts: GET cli-chat-proxy.grok.com/v1/billing: no usage in the answer")
	}
	return r, nil
}

// grokCents is {"val": n}.
type grokCents struct{ v float64 }

func (g *grokCents) UnmarshalJSON(b []byte) error {
	var x struct {
		Val number `json:"val"`
	}
	if json.Unmarshal(b, &x) == nil && x.Val.ok {
		g.v = x.Val.v
	}
	return nil
}

// grokPrepaid is the prepaid balance, {"val": cents} (a whole number,
// at or above zero; {} is zero), in dollars.
func grokPrepaid(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var x map[string]json.RawMessage
	if json.Unmarshal(raw, &x) != nil {
		return 0, false
	}
	val, ok := x["val"]
	if !ok {
		return 0, len(x) == 0
	}
	var n number
	if n.UnmarshalJSON(val) != nil || !n.ok || n.v < 0 || n.v != math.Trunc(n.v) || n.v > 1<<53 {
		return 0, false
	}
	return n.v / 100, true
}

// grokWindowName names the credit window by its length, or, without one,
// by the time to its reset: about a week or about a month.
func grokWindowName(d time.Duration, end, now time.Time) string {
	if d == 0 && !end.IsZero() {
		d = end.Sub(now)
	}
	days := math.Round(d.Hours() / 24)
	switch {
	case days >= 4 && days <= 12:
		return "week"
	case days >= 20 && days <= 45:
		return "month"
	}
	return "credits"
}

// GrokWeb reads the same credit window from grok.com's billing call
// (grok_api_v2.GrokBuildBilling/GetGrokCreditsConfig, gRPC-web) with
// grok.com's browser session, as CodexBar's GrokWebBillingFetcher.swift
// does. grok.com now asks some sessions for a key the browser holds;
// those are refused, and the CLI token reads instead.
type GrokWeb struct {
	BaseURL string
	HTTP    *http.Client
	Now     func() time.Time
}

func (GrokWeb) Endpoint() string               { return "grok" }
func (GrokWeb) Provider() eventschema.Provider { return "grok" }
func (GrokWeb) Source() string                 { return "grok-web" }

// grokCreditsRequest is GetGrokCreditsConfigRequest{} in a gRPC-web frame.
var grokCreditsRequest = []byte{0, 0, 0, 0, 2, 8, 0}

func (g GrokWeb) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	cookie = cookieHeader(cookie)
	if !isSession(cookie) {
		return usage.Reading{}, usage.ErrSkip
	}
	if cookieValue(cookie, "sso") == "" && cookieValue(cookie, "sso-rw") == "" {
		return usage.Reading{}, fmt.Errorf("%w (grok.com is read with its sso cookie)", usage.ErrAuth)
	}
	u := base(g.BaseURL, "https://grok.com") + "/grok_api_v2.GrokBuildBilling/GetGrokCreditsConfig"
	body, trailer, err := grokPost(ctx, g.HTTP, u, cookie)
	if err != nil {
		return usage.Reading{}, err
	}
	if status, msg := trailer.Get("grpc-status"), strings.ToLower(trailer.Get("grpc-message")); status != "" && status != "0" {
		switch {
		case status == "16":
			return usage.Reading{}, fmt.Errorf("%w (grpc status 16 on grok.com billing)", usage.ErrAuth)
		case status == "7" && (strings.Contains(msg, "bad-credentials") || strings.Contains(msg, "unauthenticated")):
			return usage.Reading{}, fmt.Errorf("%w (grpc status 7 on grok.com billing)", usage.ErrAuth)
		}
		return usage.Reading{}, fmt.Errorf("accounts: POST grok.com billing: grpc status %s", status)
	}
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}
	return grokWebReading(body, now())
}

// grokPost sends the gRPC-web request and returns the data frames' bytes
// and the status from the headers and the trailer frame.
func grokPost(ctx context.Context, hc *http.Client, u, cookie string) ([]byte, http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := doWebResponse(ctx, hc, http.MethodPost, u, http.Header{
		"Cookie": {cookie}, "Origin": {"https://grok.com"}, "Referer": {"https://grok.com/?_s=usage"}, "Accept": {"*/*"},
		"Content-Type": {"application/grpc-web+proto"}, "X-Grpc-Web": {"1"}, "X-User-Agent": {"connect-es/2.1.1"},
	}, grokCreditsRequest)
	if err != nil {
		return nil, nil, err
	}
	status := http.Header{}
	for _, k := range []string{"grpc-status", "grpc-message"} {
		if v := resp.header.Get(k); v != "" {
			unescaped, _ := url.PathUnescape(v)
			status.Set(k, unescaped)
		}
	}
	var data []byte
	b := resp.body
	for len(b) >= 5 {
		flag, n := b[0], int(binary.BigEndian.Uint32(b[1:5]))
		if n < 0 || 5+n > len(b) {
			return nil, nil, errors.New("accounts: POST grok.com billing: a truncated frame")
		}
		frame := b[5 : 5+n]
		switch flag {
		case 0:
			data = append(data, frame...)
		case 0x80:
			for _, line := range strings.Split(string(frame), "\r\n") {
				if k, v, ok := strings.Cut(line, ":"); ok {
					unescaped, _ := url.PathUnescape(strings.TrimSpace(v))
					status.Set(strings.ToLower(strings.TrimSpace(k)), unescaped)
				}
			}
		default:
			return nil, nil, errors.New("accounts: POST grok.com billing: an unknown frame")
		}
		b = b[5+n:]
	}
	if len(b) != 0 {
		return nil, nil, errors.New("accounts: POST grok.com billing: a truncated frame")
	}
	return data, status, nil
}

// grokWebReading reads GetGrokCreditsConfigResponse: config (1) holds the
// share used (1.1, a float), the period's end (1.5.1, Unix seconds) and
// the current period (1.8: type, start, end). An unused period omits the
// share; it is read as zero only while the current period is open.
func grokWebReading(payload []byte, now time.Time) (usage.Reading, error) {
	bad := errors.New("accounts: POST grok.com billing: unexpected answer")
	config, ok := protoField(payload, 1, 2)
	if !ok {
		return usage.Reading{}, bad
	}
	var used *float64
	if raw, ok := protoField(config, 1, 5); ok && len(raw) == 4 {
		v := float64(math.Float32frombits(binary.LittleEndian.Uint32(raw)))
		if !math.IsNaN(v) && v >= 0 && v <= 100 {
			used = &v
		}
	}
	var reset time.Time
	if m, ok := protoField(config, 5, 2); ok {
		if s, ok := protoVarint(m, 1); ok && s >= 1_700_000_000 && s <= 2_100_000_000 {
			reset = time.Unix(int64(s), 0).UTC()
		}
	}
	if used == nil {
		// No share on the wire: zero, when the answer has a reset and a
		// usage period (per-product periods, 1.6, or the current one, 1.8)
		// and the current period, when given, is open now.
		_, products := protoField(config, 6, 2)
		period, current := protoField(config, 8, 2)
		kind, _ := protoVarint(period, 1)
		current = current && (kind == 1 || kind == 2)
		if current {
			start, end := protoTimestamp(period, 2), protoTimestamp(period, 3)
			if start.IsZero() || start.After(now) || !end.After(now) {
				return usage.Reading{}, bad
			}
		}
		if reset.IsZero() || (!products && !current) {
			return usage.Reading{}, bad
		}
		zero := 0.0
		used = &zero
	}
	return usage.Reading{Scope: "account", Subscription: true, Windows: []usage.Window{{
		Name: grokWindowName(0, reset, now), UsedPct: clampPct(*used), ResetsAt: reset,
	}}}, nil
}

func protoTimestamp(msg []byte, field int) time.Time {
	m, ok := protoField(msg, field, 2)
	if !ok {
		return time.Time{}
	}
	s, ok := protoVarint(m, 1)
	if !ok || s < 1_700_000_000 || s > 2_100_000_000 {
		return time.Time{}
	}
	return time.Unix(int64(s), 0).UTC()
}

func protoVarint(msg []byte, field int) (uint64, bool) {
	raw, ok := protoField(msg, field, 0)
	if !ok {
		return 0, false
	}
	v, n := binary.Uvarint(raw)
	return v, n > 0
}

// protoField is the first occurrence of field with wire type wt in a
// protobuf message: a varint's bytes, a fixed32's 4 bytes, or a
// length-delimited field's contents.
func protoField(msg []byte, field, wt int) ([]byte, bool) {
	for len(msg) > 0 {
		tag, n := binary.Uvarint(msg)
		if n <= 0 || n > 10 {
			return nil, false
		}
		msg = msg[n:]
		f, t := int(tag>>3), int(tag&7)
		var value []byte
		switch t {
		case 0:
			_, m := binary.Uvarint(msg)
			if m <= 0 || m > 10 {
				return nil, false
			}
			value, msg = msg[:m], msg[m:]
		case 1:
			if len(msg) < 8 {
				return nil, false
			}
			value, msg = msg[:8], msg[8:]
		case 2:
			l, m := binary.Uvarint(msg)
			if m <= 0 || l > uint64(len(msg)-m) {
				return nil, false
			}
			value, msg = msg[m:m+int(l)], msg[m+int(l):]
		case 5:
			if len(msg) < 4 {
				return nil, false
			}
			value, msg = msg[:4], msg[4:]
		default:
			return nil, false
		}
		if f == field && t == wt {
			return value, true
		}
	}
	return nil, false
}
