package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerMistral registers the reader (readers_gen.go).
func readerMistral() usage.Reader { return Mistral{} }

// Mistral reads the Mistral subscription's allowances with the admin
// console's browser session, as CodexBar's Mistral provider does
// (Sources/CodexBarCore/Providers/Mistral/MistralUsageFetcher.swift,
// MistralSubscriptionBudgetParser.swift): the month's billing usage
// proves the session; the included-API and Vibe allowances' shares used
// come from the subscription page (Vibe from the console's own call when
// the page has none); the credit balance is kept in its currency.
// The billing usage's spend is priced in euros from Mistral's own price
// table and is not stored: TokenOps stores spend in dollars and does not
// convert.
type Mistral struct {
	// Admin and Console replace admin.mistral.ai and console.mistral.ai in
	// tests.
	Admin, Console string
	HTTP           *http.Client
	Now            func() time.Time
}

func (Mistral) Endpoint() string               { return "mistral" }
func (Mistral) Provider() eventschema.Provider { return eventschema.ProviderMistral }
func (Mistral) Source() string                 { return "mistral-web" }

func (m Mistral) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	if !isSession(cookie) {
		return usage.Reading{}, fmt.Errorf("%w (Mistral is read with the admin console's Cookie header)", usage.ErrAuth)
	}
	admin := base(m.Admin, "https://admin.mistral.ai")
	csrf := cookieValue(cookie, "csrftoken")
	header := http.Header{"Cookie": {cookie}, "Accept": {"*/*"}, "Origin": {"https://admin.mistral.ai"}, "Referer": {"https://admin.mistral.ai/organization/usage"}}
	if csrf != "" {
		header.Set("X-CSRFTOKEN", csrf)
	}
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	month := now().UTC()
	if _, err := doWeb(ctx, m.HTTP, http.MethodGet, fmt.Sprintf("%s/api/billing/v2/usage?month=%d&year=%d", admin, int(month.Month()), month.Year()), header, nil); err != nil {
		return usage.Reading{}, err
	}
	r := usage.Reading{Scope: "account"}
	page, err := doWeb(ctx, m.HTTP, http.MethodGet, admin+"/subscription", http.Header{
		"Cookie": {cookie}, "Accept": {"text/html"}, "Accept-Language": {"en-US,en;q=0.9"}, "Referer": {admin + "/subscription"},
	}, nil)
	var api, vibe *mistralBudget
	if err == nil {
		api, vibe = mistralBudgets(page)
	}
	if api != nil {
		r.Windows = append(r.Windows, api.window("month (API)"))
	}
	if vibe == nil && csrf != "" {
		vibe = m.vibe(ctx, cookie, csrf)
	}
	if vibe != nil {
		r.Windows = append(r.Windows, vibe.window("month (Vibe)"))
	}
	r.Subscription = len(r.Windows) > 0
	var credits struct {
		Wallet   *float64 `json:"walletAmount"`
		Notes    float64  `json:"creditNotesAmount"`
		Ongoing  float64  `json:"ongoingUsageBalance"`
		Currency string   `json:"currency"`
	}
	if body, err := doWeb(ctx, m.HTTP, http.MethodGet, admin+"/api/billing/credits", header, nil); err == nil &&
		json.Unmarshal(body, &credits) == nil && credits.Wallet != nil && strings.TrimSpace(credits.Currency) != "" {
		left := *credits.Wallet + credits.Notes - credits.Ongoing
		if cur := strings.ToUpper(strings.TrimSpace(credits.Currency)); cur == "USD" {
			r.BalanceUSD, r.HasBalance = left, true
		} else {
			r.Credits, r.CreditsUnit, r.HasCredits = left, cur, true
		}
	}
	return r, nil
}

// vibe is the Vibe allowance from the console's own call, which takes only
// the CSRF and session cookies.
func (m Mistral) vibe(ctx context.Context, cookie, csrf string) *mistralBudget {
	if strings.ContainsAny(csrf, ";,\r\n") {
		return nil
	}
	pairs := []string{"csrftoken=" + csrf}
	for _, part := range strings.Split(cookie, ";") {
		if p := strings.TrimSpace(part); strings.HasPrefix(p, "ory_session_") {
			pairs = append(pairs, p)
		}
	}
	u := base(m.Console, "https://console.mistral.ai") + "/api-ui/trpc/billing.vibeUsage?batch=1&input=%7B%220%22%3A%7B%22json%22%3Anull%2C%22meta%22%3A%7B%22values%22%3A%5B%22undefined%22%5D%2C%22v%22%3A1%7D%7D%7D"
	body, err := doWeb(ctx, m.HTTP, http.MethodGet, u, http.Header{"Cookie": {strings.Join(pairs, "; ")}, "Accept": {"*/*"}, "X-Csrftoken": {csrf}}, nil)
	if err != nil {
		return nil
	}
	var resp []struct {
		Result struct {
			Data struct {
				JSON struct {
					UsagePercentage *float64 `json:"usagePercentage"`
					ResetAt         string   `json:"resetAt"`
				} `json:"json"`
			} `json:"data"`
		} `json:"result"`
	}
	if json.Unmarshal(body, &resp) != nil || len(resp) == 0 {
		return nil
	}
	j := resp[0].Result.Data.JSON
	if j.UsagePercentage == nil || *j.UsagePercentage < 0 || *j.UsagePercentage > 100 {
		return nil
	}
	return &mistralBudget{UsagePercentage: *j.UsagePercentage, InitialBudget: 1, ResetAt: j.ResetAt}
}

// mistralBudget is one allowance as the subscription page carries it.
type mistralBudget struct {
	UsagePercentage float64 `json:"usage_percentage"`
	InitialBudget   float64 `json:"initial_budget"`
	Currency        string  `json:"currency"`
	ResetAt         string  `json:"reset_at"`
}

func (b mistralBudget) window(name string) usage.Window {
	return usage.Window{Name: name, UsedPct: clampPct(b.UsagePercentage), ResetsAt: parseTime(b.ResetAt)}
}

// mistralBudgets reads the API and Vibe allowances from the subscription
// page's React Server Components payload (self.__next_f.push chunks). Two
// differing budgets are ambiguous and read as none.
func mistralBudgets(html []byte) (api, vibe *mistralBudget) {
	var stream bytes.Buffer
	marker := []byte("self.__next_f.push(")
	for rest := html; ; {
		i := bytes.Index(rest, marker)
		if i < 0 {
			break
		}
		rest = bytes.TrimLeft(rest[i+len(marker):], " \t\r\n")
		end := jsonEnd(rest)
		if end < 0 {
			continue
		}
		var arr []any
		if json.Unmarshal(rest[:end], &arr) == nil && len(arr) >= 2 {
			if ch, ok := arr[0].(float64); ok && ch == 1 {
				if s, ok := arr[1].(string); ok {
					stream.WriteString(s)
				}
			}
		}
		rest = rest[end:]
	}
	type pair struct{ api, vibe *mistralBudget }
	var found []pair
	data := stream.Bytes()
	for len(data) > 0 {
		line := data
		next := []byte(nil)
		if nl := bytes.IndexByte(data, '\n'); nl >= 0 {
			line, next = data[:nl], data[nl+1:]
		}
		colon := bytes.IndexByte(line, ':')
		if colon <= 0 || !isHex(line[:colon]) || colon+1 >= len(line) {
			data = next
			continue
		}
		payload := data[colon+1:]
		if strings.IndexByte("TAOoUSsLlGgMmV", payload[0]) >= 0 {
			// A byte-counted row ("T<hex length>,<bytes>") may hold
			// newlines and text that looks like rows: skip it whole.
			comma := bytes.IndexByte(payload, ',')
			if comma < 2 || !isHex(payload[1:comma]) {
				return nil, nil
			}
			n, err := strconv.ParseInt(string(payload[1:comma]), 16, 64)
			if err != nil || int(n) > len(payload)-comma-1 {
				return nil, nil
			}
			data = payload[comma+1+int(n):]
			continue
		}
		if payload[0] == '{' || payload[0] == '[' {
			var root any
			if json.Unmarshal(line[colon+1:], &root) == nil {
				walk(root, func(m map[string]any) bool {
					raw, ok := m["budget"].(map[string]any)
					if !ok {
						return false
					}
					p := pair{mistralBudgetOf(raw["api_budget"]), mistralBudgetOf(raw["vibe_budget"])}
					if p.api != nil || p.vibe != nil {
						found = append(found, p)
					}
					return false
				})
			}
		}
		data = next
	}
	if len(found) == 0 {
		return nil, nil
	}
	for _, p := range found[1:] {
		if !sameBudget(p.api, found[0].api) || !sameBudget(p.vibe, found[0].vibe) {
			return nil, nil
		}
	}
	return found[0].api, found[0].vibe
}

func mistralBudgetOf(v any) *mistralBudget {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	var b mistralBudget
	if json.Unmarshal([]byte(mustJSON(m)), &b) != nil || b.UsagePercentage < 0 || b.InitialBudget <= 0 || strings.TrimSpace(b.Currency) == "" {
		return nil
	}
	return &b
}

func sameBudget(a, b *mistralBudget) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func isHex(b []byte) bool {
	for _, c := range b {
		hex := c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
		if !hex {
			return false
		}
	}
	return len(b) > 0
}

// jsonEnd is the end of the JSON array or object b starts with, -1 when
// it does not close.
func jsonEnd(b []byte) int {
	if len(b) == 0 || b[0] != '[' && b[0] != '{' {
		return -1
	}
	var closers []byte
	inString, escaped := false, false
	for i, c := range b {
		switch {
		case inString && escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case inString && c == '"':
			inString = false
		case inString:
		case c == '"':
			inString = true
		case c == '[':
			closers = append(closers, ']')
		case c == '{':
			closers = append(closers, '}')
		case c == ']' || c == '}':
			if len(closers) == 0 || closers[len(closers)-1] != c {
				return -1
			}
			closers = closers[:len(closers)-1]
			if len(closers) == 0 {
				return i + 1
			}
		}
	}
	return -1
}
