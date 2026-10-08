package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerKiroOverage registers the reader (readers_gen.go).
func readerKiroOverage() usage.Reader { return KiroOverage{} }

// KiroOverage reads Kiro's credit usage with overage from the CodeWhisperer
// GetUsageLimits API, as CodexBar's Kiro provider enriches its CLI reading:
// the plan's credits used this month and, when overage is enabled, the
// overage credits used against their cap. It reads with kiro-cli's own
// sign-in and profile from kiro-cli's database, only once granted (ADR
// 0013); kiro-cli renews that token, never this reader.
type KiroOverage struct {
	// BaseURL replaces the regional endpoint, for tests.
	BaseURL string
	HTTP    *http.Client
}

func (KiroOverage) Endpoint() string               { return "kiro-overage" }
func (KiroOverage) Provider() eventschema.Provider { return "kiro" }
func (KiroOverage) Source() string                 { return "kiro-overage" }

// kiroEndpoints are the regions GetUsageLimits is served from, by the
// profile ARN's region.
var kiroEndpoints = map[string]string{
	"us-east-1":    "https://codewhisperer.us-east-1.amazonaws.com",
	"eu-central-1": "https://q.eu-central-1.amazonaws.com",
}

// Read reads with the granted sign-in; the token is kiro-cli's access
// token and profile ARN, as applogin reads them.
func (k KiroOverage) Read(ctx context.Context, token string) (usage.Reading, error) {
	return k.ReadAppLogin(ctx, token)
}

func (k KiroOverage) ReadAppLogin(ctx context.Context, token string) (usage.Reading, error) {
	var id struct {
		AccessToken string `json:"access_token"`
		ProfileARN  string `json:"profile_arn"`
	}
	if json.Unmarshal([]byte(token), &id) != nil || id.AccessToken == "" {
		return usage.Reading{}, fmt.Errorf("%w (no kiro-cli sign-in; run kiro-cli login)", usage.ErrAuth)
	}
	root, ok := kiroEndpoint(id.ProfileARN)
	if !ok {
		return usage.Reading{}, errors.New("accounts: kiro-cli's profile is not in a region GetUsageLimits serves")
	}
	var resp struct {
		NextDateReset        stamp `json:"nextDateReset"`
		OverageConfiguration *struct {
			OverageStatus string `json:"overageStatus"`
		} `json:"overageConfiguration"`
		UsageBreakdownList []struct {
			ResourceType  string `json:"resourceType"`
			Limit         number `json:"usageLimitWithPrecision"`
			Usage         number `json:"currentUsageWithPrecision"`
			Overages      number `json:"currentOveragesWithPrecision"`
			OverageCap    number `json:"overageCapWithPrecision"`
			NextDateReset stamp  `json:"nextDateReset"`
		} `json:"usageBreakdownList"`
	}
	header := http.Header{
		"Authorization": {"Bearer " + id.AccessToken},
		"X-Amz-Target":  {"AmazonCodeWhispererService.GetUsageLimits"},
		"Content-Type":  {"application/x-amz-json-1.0"},
	}
	body, err := json.Marshal(map[string]string{"profileArn": id.ProfileARN})
	if err != nil {
		return usage.Reading{}, err
	}
	if err := doJSON(ctx, k.HTTP, http.MethodPost, base(k.BaseURL, root)+"/", header, body, &resp); err != nil {
		return usage.Reading{}, err
	}
	r := usage.Reading{Scope: "account"}
	for _, c := range resp.UsageBreakdownList {
		if c.ResourceType != "CREDIT" || !c.Limit.ok || c.Limit.v <= 0 || !c.Usage.ok {
			continue
		}
		reset := c.NextDateReset.t
		if reset.IsZero() {
			reset = resp.NextDateReset.t
		}
		// Usage counts overage too: the plan's share is what is left.
		plan := c.Usage.v - c.Overages.v
		r.Windows = append(r.Windows, usage.Window{Name: "month", UsedPct: clampPct(pct(plan, c.Limit.v)),
			Duration: 30 * 24 * time.Hour, ResetsAt: reset})
		enabled := resp.OverageConfiguration != nil && strings.EqualFold(resp.OverageConfiguration.OverageStatus, "ENABLED")
		if enabled && c.OverageCap.ok && c.OverageCap.v > 0 {
			r.Windows = append(r.Windows, usage.Window{Name: "overage", UsedPct: clampPct(pct(c.Overages.v, c.OverageCap.v)), ResetsAt: reset})
		}
		break
	}
	r.Subscription = len(r.Windows) > 0
	return r, nil
}

// kiroEndpoint is the regional endpoint for a CodeWhisperer profile ARN
// (arn:aws:codewhisperer:<region>:<account>:profile/<id>).
func kiroEndpoint(arn string) (string, bool) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) != 6 || parts[0] != "arn" || parts[1] != "aws" || parts[2] != "codewhisperer" ||
		!strings.HasPrefix(parts[5], "profile/") || len(parts[5]) == len("profile/") || strings.ContainsAny(arn, " \t\r\n") {
		return "", false
	}
	root, ok := kiroEndpoints[parts[3]]
	return root, ok
}
