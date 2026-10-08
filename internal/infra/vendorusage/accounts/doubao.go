package accounts

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerDoubao registers the reader (readers_gen.go).
func readerDoubao() usage.Reader { return Doubao{} }

// Doubao reads Volcengine Ark's Coding Plan windows with GetCodingPlanUsage
// on Volcengine's OpenAPI, signed with an AccessKey pair, as CodexBar's
// Doubao provider does; an account with no Coding Plan windows is read for
// its Agent Plan (GetAFPUsage) instead. The credential is
// "ACCESS_KEY_ID:SECRET_ACCESS_KEY", optionally ":REGION" (cn-beijing by
// default). An Ark inference API key cannot read plan usage: CodexBar
// learns of it only by sending a chat completion, which spends tokens, so
// TokenOps does not.
type Doubao struct {
	BaseURL string
	HTTP    *http.Client
	// Now stamps the signature; tests fix it.
	Now func() time.Time
}

func (Doubao) Endpoint() string               { return "doubao" }
func (Doubao) Provider() eventschema.Provider { return "doubao" }
func (Doubao) Source() string                 { return "doubao-account" }

// doubaoLevels maps the vendor's quota levels onto window lengths.
var doubaoLevels = map[string]time.Duration{
	"session": 5 * time.Hour, "5-hour": 5 * time.Hour, "five_hour": 5 * time.Hour, "5h": 5 * time.Hour,
	"weekly": 7 * 24 * time.Hour, "week": 7 * 24 * time.Hour,
	"monthly": 30 * 24 * time.Hour, "month": 30 * 24 * time.Hour,
}

func (d Doubao) Read(ctx context.Context, credential string) (usage.Reading, error) {
	ak, sk, region, ok := doubaoCredential(credential)
	if !ok {
		return usage.Reading{}, fmt.Errorf("%w (Doubao needs ACCESS_KEY_ID:SECRET_ACCESS_KEY)", usage.ErrAuth)
	}
	root := base(d.BaseURL, "https://open.volcengineapi.com")
	var coding struct {
		Result struct {
			QuotaUsage []struct {
				Level          string  `json:"Level"`
				Percent        number  `json:"Percent"`
				ResetTimestamp float64 `json:"ResetTimestamp"`
			} `json:"QuotaUsage"`
		} `json:"Result"`
	}
	if err := d.call(ctx, root, "GetCodingPlanUsage", ak, sk, region, &coding); err != nil {
		return usage.Reading{}, err
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	for _, q := range coding.Result.QuotaUsage {
		dur, ok := doubaoLevels[strings.ToLower(q.Level)]
		if !ok || !q.Percent.ok {
			continue
		}
		r.Windows = append(r.Windows, usage.Window{Name: windowName(dur), UsedPct: clampPct(q.Percent.v), Duration: dur, ResetsAt: unixTime(q.ResetTimestamp)})
	}
	if len(r.Windows) > 0 {
		return r, nil
	}
	var agent struct {
		Result map[string]*struct {
			Quota     float64 `json:"Quota"`
			Used      float64 `json:"Used"`
			ResetTime float64 `json:"ResetTime"`
		} `json:"Result"`
	}
	if err := d.call(ctx, root, "GetAFPUsage", ak, sk, region, &agent); err != nil {
		var se *statusError
		if errors.Is(err, usage.ErrAuth) || (errors.As(err, &se) && se.status == http.StatusNotFound) {
			return usage.Reading{Scope: "account"}, nil // no Agent Plan, and no Coding Plan windows
		}
		return usage.Reading{}, err
	}
	for _, w := range []struct {
		key string
		d   time.Duration
	}{{"AFPFiveHour", 5 * time.Hour}, {"AFPWeekly", 7 * 24 * time.Hour}, {"AFPMonthly", 30 * 24 * time.Hour}} {
		q := agent.Result[w.key]
		if q == nil || q.Quota <= 0 {
			continue
		}
		r.Windows = append(r.Windows, usage.Window{Name: windowName(w.d), UsedPct: clampPct(pct(q.Used, q.Quota)), Duration: w.d, ResetsAt: unixTime(q.ResetTime / 1000)})
	}
	if len(r.Windows) == 0 {
		return usage.Reading{Scope: "account"}, nil
	}
	return r, nil
}

// call POSTs an empty body to action, signed with the AccessKey pair.
func (d Doubao) call(ctx context.Context, root, action, ak, sk, region string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	u := root + "/?Action=" + action + "&Version=2024-01-01"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	volcengineSign(req, nil, ak, sk, region, now())
	hc := d.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("accounts: POST %s %s: %w", hostPath(u), action, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w (%d on %s)", usage.ErrAuth, resp.StatusCode, action)
	case resp.StatusCode != http.StatusOK:
		return &statusError{method: http.MethodPost, where: action, status: resp.StatusCode}
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("accounts: POST %s: %w", action, err)
	}
	return nil
}

func doubaoCredential(c string) (ak, sk, region string, ok bool) {
	parts := strings.Split(strings.TrimSpace(c), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return "", "", "", false
	}
	ak, sk, region = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), "cn-beijing"
	if len(parts) == 3 && strings.TrimSpace(parts[2]) != "" {
		region = strings.TrimSpace(parts[2])
	}
	return ak, sk, region, ak != "" && sk != ""
}

func clampPct(v float64) float64 {
	return max(0, min(100, v))
}

// unixTime is a Unix time in seconds; zero or less is none.
func unixTime(s float64) time.Time {
	if s <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(s), 0).UTC()
}

// volcengineSign signs req with Volcengine's V4 scheme for the "ark"
// service: HMAC-SHA256 over the canonical request, with the key derived
// from the secret, the date, the region, the service and "request".
func volcengineSign(req *http.Request, body []byte, ak, sk, region string, now time.Time) {
	now = now.UTC()
	stamp, day := now.Format("20060102T150405Z"), now.Format("20060102")
	sum := sha256.Sum256(body)
	payload := hex.EncodeToString(sum[:])
	contentType := req.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/x-www-form-urlencoded; charset=utf-8"
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("X-Date", stamp)
	req.Header.Set("X-Content-Sha256", payload)
	host := req.URL.Host
	req.Host = host
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	const signed = "content-type;host;x-content-sha256;x-date"
	canonical := strings.Join([]string{
		req.Method, path, volcengineQuery(req.URL.Query()),
		"content-type:" + contentType, "host:" + host, "x-content-sha256:" + payload, "x-date:" + stamp, "",
		signed, payload,
	}, "\n")
	scope := day + "/" + region + "/ark/request"
	creq := sha256.Sum256([]byte(canonical))
	toSign := "HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hex.EncodeToString(creq[:])
	k := hmacSHA256([]byte(sk), day)
	for _, part := range []string{region, "ark", "request"} {
		k = hmacSHA256(k, part)
	}
	sig := hex.EncodeToString(hmacSHA256(k, toSign))
	req.Header.Set("Authorization", "HMAC-SHA256 Credential="+ak+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func hmacSHA256(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// volcengineQuery is the canonical query string: each name and value
// percent-encoded (only A-Z a-z 0-9 - _ . ~ left), sorted.
func volcengineQuery(q url.Values) string {
	var pairs []string
	for k, vs := range q {
		for _, v := range vs {
			pairs = append(pairs, volcengineEscape(k)+"="+volcengineEscape(v))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

func volcengineEscape(s string) string {
	var b bytes.Buffer
	for _, c := range []byte(s) {
		if ('A' <= c && c <= 'Z') || ('a' <= c && c <= 'z') || ('0' <= c && c <= '9') || strings.IndexByte("-_.~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
