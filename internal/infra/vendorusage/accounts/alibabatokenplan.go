package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerAlibabaTokenPlan registers the reader (readers_gen.go).
func readerAlibabaTokenPlan() usage.Reader { return AlibabaTokenPlan{} }

// AlibabaTokenPlan reads Alibaba Cloud's Token Plan from the Model Studio
// (international) or Bailian (mainland) console with its browser session,
// as CodexBar's Alibaba Token Plan provider does
// (Sources/CodexBarCore/Providers/Alibaba/AlibabaTokenPlanUsageFetcher.swift):
// the Personal/Solo plan's 5-hour, weekly and monthly windows, or, for a
// Team plan, the credit pool used against its total until the cycle
// resets. CodexBar asks the operator which region and variant; this reads
// Personal then Team on the international console, then the mainland one.
type AlibabaTokenPlan struct {
	// BaseURLs replace the regions' hosts in tests, in order.
	BaseURLs []string
	HTTP     *http.Client
}

func (AlibabaTokenPlan) Endpoint() string               { return "alibabatokenplan" }
func (AlibabaTokenPlan) Provider() eventschema.Provider { return "alibabatokenplan" }
func (AlibabaTokenPlan) Source() string                 { return "alibabatokenplan-web" }

// alibabaTokenRegion is one region's console for the Token Plan.
type alibabaTokenRegion struct {
	personal                       tokenPlanConsole
	teamProduct, dashboard, origin string
}

var alibabaTokenRegions = []alibabaTokenRegion{
	{
		personal: tokenPlanConsole{
			origin: "https://modelstudio.console.alibabacloud.com", data: "https://bailian-singapore-cs.alibabacloud.com",
			dashboard: "https://modelstudio.console.alibabacloud.com/ap-southeast-1/?tab=plan#/efm/subscription/token-plan/personal",
			action:    "IntlBroadScopeAspnGateway", regionID: "ap-southeast-1",
		},
		teamProduct: "sfm_tokenplanteams_dp_intl", origin: "https://modelstudio.console.alibabacloud.com",
		dashboard: "https://modelstudio.console.alibabacloud.com/ap-southeast-1/?tab=plan#/efm/subscription/token-plan",
	},
	{
		personal: tokenPlanConsole{
			origin: "https://bailian.console.aliyun.com", data: "https://bailian-cs.console.aliyun.com",
			dashboard: "https://bailian.console.aliyun.com/cn-beijing?tab=plan#/efm/subscription/token-plan/personal",
			action:    "BroadScopeAspnGateway", regionID: "cn-beijing",
		},
		teamProduct: "sfm_tokenplanteams_dp_cn", origin: "https://bailian.console.aliyun.com",
		dashboard: "https://bailian.console.aliyun.com/cn-beijing?tab=plan#/efm/subscription/token-plan",
	},
}

func (a AlibabaTokenPlan) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	if !isSession(cookie) {
		return usage.Reading{}, fmt.Errorf("%w (the Token Plan is read with the console's Cookie header, not a key)", usage.ErrAuth)
	}
	var err error
	answered := false
	for i, r := range alibabaTokenRegions {
		if i < len(a.BaseURLs) && a.BaseURLs[i] != "" {
			b := strings.TrimRight(a.BaseURLs[i], "/")
			r.origin, r.personal.origin, r.personal.data = b, b, b
		}
		token := consoleSecToken(ctx, a.HTTP, r.origin, r.personal.dashboard, cookie)
		for _, read := range []func() (usage.Reading, error){
			func() (usage.Reading, error) { return readTokenPlanWindows(ctx, a.HTTP, r.personal, cookie, token) },
			func() (usage.Reading, error) { return a.team(ctx, r, cookie, token) },
		} {
			var got usage.Reading
			got, err = read()
			switch {
			case err == nil:
				return got, nil
			case errors.Is(err, errOtherRegion):
				answered = true
				continue // no plan of this variant here
			case errors.Is(err, usage.ErrAuth):
			default:
				return usage.Reading{}, err
			}
			break // this console refused the session: try the other region's
		}
	}
	if answered {
		// A console accepted the session and has no Token Plan for it.
		return usage.Reading{Scope: "account"}, nil
	}
	return usage.Reading{}, err
}

// team reads a Team plan's credit pool from GetSubscriptionSummary.
func (a AlibabaTokenPlan) team(ctx context.Context, r alibabaTokenRegion, cookie, token string) (usage.Reading, error) {
	fields := [][2]string{{"product", "BssOpenAPI-V3"}, {"action", "GetSubscriptionSummary"},
		{"params", mustJSON(map[string]string{"ProductCode": r.teamProduct})}, {"region", r.personal.regionID}}
	if token != "" {
		fields = append(fields, [2]string{"sec_token", token})
	}
	q := url.Values{"action": {"GetSubscriptionSummary"}, "product": {"BssOpenAPI-V3"}, "_tag": {""}}
	body, err := doWeb(ctx, a.HTTP, http.MethodPost, r.origin+"/data/api.json?"+q.Encode(), consoleHeader(cookie, r.origin, r.dashboard), formBody(fields))
	if err != nil {
		return usage.Reading{}, err
	}
	return parseTokenPlanSummary(body)
}

var (
	summaryUsed      = []string{"usedQuota", "used_quota", "usedCredits", "usedCredit", "consumedCredits", "usage", "used", "usedAmount", "consumeAmount", "usedValue", "UsedValue", "consumedValue", "ConsumedValue"}
	summaryTotal     = []string{"totalQuota", "total_quota", "totalCredits", "totalCredit", "quota", "creditLimit", "creditsTotal", "monthlyTotalQuota", "amount", "totalValue", "TotalValue", "cycleTotalValue", "CycleTotalValue"}
	summaryRemaining = []string{"remainingQuota", "remainQuota", "remainingCredits", "remainingCredit", "availableCredits", "balance", "remaining", "availableAmount", "remainAmount", "totalSurplusValue", "TotalSurplusValue", "surplusValue", "SurplusValue", "cycleSurplusValue", "CycleSurplusValue"}
	summaryCount     = []string{"totalCount", "TotalCount", "subscriptionTotalNumber", "SubscriptionTotalNumber"}
	summaryReset     = []string{"nextRefreshTime", "NextCycleFlushTime", "resetTime", "periodEndTime", "billingCycleEnd", "billCycleEndTime", "expireTime", "expirationTime", "endTime", "validEndTime", "instanceEndTime", "EndTime", "cycleEndTime", "CycleEndTime", "nearestExpireDate", "NearestExpireDate"}
)

// parseTokenPlanSummary maps a subscription summary's credit pool to a
// "credits" window: used (or total less remaining) of total, resetting at
// the cycle's end. A summary with no subscription is errOtherRegion.
func parseTokenPlanSummary(body []byte) (usage.Reading, error) {
	v, ok := decodeConsole(body)
	if !ok {
		if looksLikeSignIn(body) {
			return usage.Reading{}, fmt.Errorf("%w (the console asked to sign in)", usage.ErrAuth)
		}
		return usage.Reading{}, errors.New("accounts: token plan: the answer is not JSON")
	}
	if err := consoleError(v); err != nil {
		return usage.Reading{}, err
	}
	keys := append(append(append([]string{}, summaryUsed...), summaryTotal...), summaryRemaining...)
	summary := objectWith(v, keys...)
	if summary == nil {
		summary = objectWith(v, summaryCount...)
	}
	if summary == nil {
		return usage.Reading{}, fmt.Errorf("accounts: token plan: no subscription summary in the answer")
	}
	if n, ok := numOf(field(summary, summaryCount...)); ok && n == 0 {
		return usage.Reading{}, errOtherRegion
	}
	total, hasTotal := numOf(field(summary, summaryTotal...))
	if !hasTotal || total <= 0 {
		return usage.Reading{}, errOtherRegion
	}
	used, hasUsed := numOf(field(summary, summaryUsed...))
	if !hasUsed {
		remaining, ok := numOf(field(summary, summaryRemaining...))
		if !ok {
			return usage.Reading{}, errors.New("accounts: token plan: the summary has a total but no use")
		}
		used = max(0, total-remaining)
	}
	reset := timeOf(field(summary, summaryReset...))
	if reset.IsZero() {
		reset = timeOf(field(objectWith(v, summaryReset...), summaryReset...))
	}
	return usage.Reading{Scope: "account", Subscription: true, Windows: []usage.Window{
		{Name: "credits", UsedPct: clampPct(pct(used, total)), ResetsAt: reset},
	}}, nil
}
