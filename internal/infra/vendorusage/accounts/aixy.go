package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// gatewayAixy registers the gateway (gateways_gen.go).
func gatewayAixy() usage.Gateway { return Aixy{} }

// Aixy reads GET /v1/usage on the Aixy gateway (docs.aixy-gateway.com,
// observe/usage), hosted or self-hosted, with a project API key, as
// CodexBar's Aixy provider does: the budgets that apply to the key (key,
// user, team, project, organisation; daily to lifetime), each with what
// is spent against it. Overlapping budgets are never summed. Its 7-day
// attributed spend is an estimate over a rolling week, not a billing
// period, and is not read.
type Aixy struct {
	// HTTP is the client; nil uses a default.
	HTTP *http.Client
}

func (Aixy) Name() string   { return "aixy" }
func (Aixy) Source() string { return "aixy-account" }

// aixyHost is the hosted gateway.
const aixyHost = "api.aixy-gateway.com"

// Recognise knows the hosted gateway by its host; a self-hosted one has no
// route that names it without a key, so it is read only when named.
func (Aixy) Recognise(_ context.Context, root string) bool {
	u, err := url.Parse(root)
	return err == nil && u.Hostname() == aixyHost
}

var aixyIntervals = map[string]time.Duration{"daily": 24 * time.Hour, "weekly": 7 * 24 * time.Hour, "monthly": 30 * 24 * time.Hour}

// errAixyContract is an answer in a contract this version does not read.
var errAixyContract = errors.New("accounts: Aixy answered with an unsupported usage contract")

type aixyBudget struct {
	Scope        string `json:"scope"`
	Interval     string `json:"interval"`
	Enforcement  string `json:"enforcement"`
	Shared       bool   `json:"shared"`
	LimitUSD     number `json:"limit_usd"`
	SpendStatus  string `json:"spend_status"`
	SpendUSD     number `json:"spend_usd"`
	RemainingUSD number `json:"remaining_usd"`
	StartsAt     string `json:"starts_at"`
	ResetsAt     string `json:"resets_at"`
	Availability struct {
		Status       string `json:"status"`
		SpentUSD     number `json:"spent_usd"`
		ReservedUSD  number `json:"reserved_usd"`
		RemainingUSD number `json:"remaining_usd"`
	} `json:"availability"`
}

// used is what counts against a budget, when Aixy knows it: settled spend
// plus outstanding reservations for a hard budget, recorded spend for a
// monitoring one.
func (b aixyBudget) used() (used, remaining float64, ok bool) {
	if b.Enforcement == "hard" {
		a := b.Availability
		if a.Status != "available" || !a.SpentUSD.ok || !a.ReservedUSD.ok || !a.RemainingUSD.ok {
			return 0, 0, false
		}
		return a.SpentUSD.v + a.ReservedUSD.v, a.RemainingUSD.v, true
	}
	if b.SpendStatus != "available" || !b.SpendUSD.ok {
		return 0, 0, false
	}
	return b.SpendUSD.v, b.RemainingUSD.v, true
}

func (g Aixy) Read(ctx context.Context, root, key string) (usage.Reading, error) {
	var resp struct {
		Object   string       `json:"object"`
		Currency string       `json:"currency"`
		Budgets  []aixyBudget `json:"budgets"`
	}
	if err := getGateway(ctx, g.HTTP, root+"/v1/usage", "Authorization", "Bearer "+key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.Object != "key.usage" || resp.Currency != "USD" {
		return usage.Reading{}, errAixyContract
	}
	type known struct {
		b                       aixyBudget
		used, remaining, usedPc float64
	}
	var budgets []known
	for _, b := range resp.Budgets {
		if !b.LimitUSD.ok || b.LimitUSD.v <= 0 {
			continue
		}
		used, remaining, ok := b.used()
		if !ok {
			continue // Aixy does not know its balance: not shown as zero
		}
		budgets = append(budgets, known{b, used, remaining, pct(used, b.LimitUSD.v)})
	}
	// Hard limits first, then the most used, as CodexBar ranks them.
	sort.SliceStable(budgets, func(i, j int) bool {
		hi, hj := budgets[i].b.Enforcement == "hard", budgets[j].b.Enforcement == "hard"
		if hi != hj {
			return hi
		}
		return budgets[i].usedPc > budgets[j].usedPc
	})
	r := usage.Reading{Scope: "key"}
	for i, k := range budgets {
		w := usage.Window{Name: aixyBudgetName(k.b), UsedPct: k.usedPc, ResetsAt: parseTime(k.b.ResetsAt)}
		if start := parseTime(k.b.StartsAt); !start.IsZero() && w.ResetsAt.After(start) {
			w.Duration = w.ResetsAt.Sub(start)
		} else {
			w.Duration = aixyIntervals[k.b.Interval]
		}
		r.Windows = append(r.Windows, w)
		if k.b.Enforcement == "hard" && k.remaining <= 0 {
			r.LimitReached = true
		}
		// The binding budget is the spend against a cap.
		if i == 0 {
			r.UsedUSD, r.HasUsed, r.LimitUSD = k.used, true, k.b.LimitUSD.v
		}
	}
	return r, nil
}

// aixyBudgetName says what a budget covers: "project monthly", "key
// lifetime (monitor)".
func aixyBudgetName(b aixyBudget) string {
	scope := b.Scope
	if scope == "api_key" {
		scope = "key"
	}
	name := scope + " " + b.Interval
	if b.Shared {
		name += " (shared)"
	}
	if b.Enforcement != "hard" {
		name += " (monitor)"
	}
	return name
}
