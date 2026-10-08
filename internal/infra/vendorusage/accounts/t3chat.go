package accounts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerT3Chat registers the reader (readers_gen.go).
func readerT3Chat() usage.Reader { return T3Chat{} }

// T3Chat reads the usage t3.chat shows its signed-in users, ported from
// CodexBar (Sources/CodexBarCore/Resources/Plugins/t3chat.js,
// Providers/T3Chat, docs/t3chat.md): the web app's tRPC call
// getCustomerData, with the browser session's Cookie header pasted at
// setup. T3 Chat publishes no API for it.
//
// The 4-hour Base bucket is the "4h" window; the monthly Overage budget is
// the "month" window, resetting at the subscription's period end.
type T3Chat struct {
	BaseURL string
	HTTP    *http.Client
}

func (T3Chat) Endpoint() string               { return "t3chat" }
func (T3Chat) Provider() eventschema.Provider { return "t3chat" }
func (T3Chat) Source() string                 { return "t3chat-account" }

const t3ChatInput = `{"0":{"json":{"sessionId":null},"meta":{"values":{"sessionId":["undefined"]}}}}`

func (t T3Chat) Read(ctx context.Context, key string) (usage.Reading, error) {
	cookie := cookieHeader(key)
	if cookie == "" || !strings.Contains(cookie, "=") {
		return usage.Reading{}, fmt.Errorf("%w (the T3 Chat credential is not a Cookie header)", usage.ErrAuth)
	}
	header := http.Header{
		"Accept":          {"*/*"},
		"Accept-Language": {"en-US,en;q=0.9"},
		"User-Agent":      {browserUA},
		"Sec-Fetch-Dest":  {"empty"},
		"Sec-Fetch-Mode":  {"cors"},
		"Sec-Fetch-Site":  {"same-origin"},
		"Referer":         {"https://t3.chat/settings/customization"},
		"Trpc-Accept":     {"application/jsonl"},
		"X-Trpc-Source":   {"web-client"},
		"X-Trpc-Batch":    {"true"},
		"Cookie":          {cookie},
		"Origin":          {"https://t3.chat"},
	}
	u := base(t.BaseURL, "https://t3.chat") + "/api/trpc/getCustomerData?batch=1&input=" + url.QueryEscape(t3ChatInput)
	body, err := getPage(ctx, t.HTTP, u, header)
	var se *statusError
	if errors.As(err, &se) && se.status == http.StatusTooManyRequests {
		return usage.Reading{}, fmt.Errorf("%w (T3 Chat's edge asked for a browser check; paste a fresh Cookie header)", err)
	}
	if err != nil {
		return usage.Reading{}, err
	}
	data := t3ChatCustomer(body)
	if data == nil {
		return usage.Reading{}, errors.New("accounts: t3chat getCustomerData: unrecognised answer")
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	four, _ := data["usageFourHourPercentage"].(float64)
	reset := t3ChatTime(data["usageFourHourNextResetAt"])
	if reset.IsZero() {
		reset = t3ChatTime(data["usageWindowNextResetAt"])
	}
	r.Windows = append(r.Windows, usage.Window{Name: "4h", Duration: 4 * time.Hour, UsedPct: clampPct(four), ResetsAt: reset})
	month, ok := data["usageMonthPercentage"].(float64)
	if !ok {
		month, _ = data["usagePeriodPercentage"].(float64)
	}
	var end time.Time
	if sub, ok := data["subscription"].(map[string]any); ok {
		end = t3ChatTime(sub["currentPeriodEnd"])
	}
	r.Windows = append(r.Windows, usage.Window{Name: "month", UsedPct: clampPct(month), ResetsAt: end})
	return r, nil
}

// t3ChatCustomer finds the customer data in the JSONL answer: the first
// object, at any depth, that carries the usage percentages.
func t3ChatCustomer(body []byte) map[string]any {
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var v any
		if json.Unmarshal(sc.Bytes(), &v) != nil {
			continue
		}
		if found := t3ChatFind(v, 0); found != nil {
			return found
		}
	}
	return nil
}

func t3ChatFind(v any, depth int) map[string]any {
	if depth > 32 {
		return nil
	}
	switch x := v.(type) {
	case map[string]any:
		_, four := x["usageFourHourPercentage"].(float64)
		_, month := x["usageMonthPercentage"].(float64)
		_, sub := x["subscription"]
		_, band := x["usageBand"]
		if four || month || (sub && band) {
			return x
		}
		for _, c := range x {
			if f := t3ChatFind(c, depth+1); f != nil {
				return f
			}
		}
	case []any:
		for _, c := range x {
			if f := t3ChatFind(c, depth+1); f != nil {
				return f
			}
		}
	}
	return nil
}

// t3ChatTime reads a time given in Unix milliseconds or seconds.
func t3ChatTime(v any) time.Time {
	n, ok := v.(float64)
	switch {
	case !ok || n <= 0:
		return time.Time{}
	case n > 1e10:
		return time.UnixMilli(int64(n)).UTC()
	}
	return time.Unix(int64(n), 0).UTC()
}
