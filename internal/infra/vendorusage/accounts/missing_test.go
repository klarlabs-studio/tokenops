package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// without is a recorded answer with the fields at paths left out. A path
// is the keys down to the field, joined by dots; a number is an index.
// Numbers keep the spelling the vendor gave them.
func without(t *testing.T, body string, paths ...string) string {
	t.Helper()
	return edited(t, body, paths, func(obj map[string]any, field string) { delete(obj, field) })
}

// nulled is a recorded answer with the fields at paths sent as null.
func nulled(t *testing.T, body string, paths ...string) string {
	t.Helper()
	return edited(t, body, paths, func(obj map[string]any, field string) { obj[field] = nil })
}

// garbled is a recorded answer with the fields at paths sent as text
// that is no number.
func garbled(t *testing.T, body string, paths ...string) string {
	t.Helper()
	return edited(t, body, paths, func(obj map[string]any, field string) { obj[field] = "n/a" })
}

func edited(t *testing.T, body string, paths []string, edit func(obj map[string]any, field string)) string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("answer is not JSON: %v", err)
	}
	for _, path := range paths {
		keys := strings.Split(path, ".")
		at := doc
		for _, k := range keys[:len(keys)-1] {
			switch node := at.(type) {
			case map[string]any:
				at = node[k]
			case []any:
				i, err := strconv.Atoi(k)
				if err != nil || i >= len(node) {
					t.Fatalf("%s: no element %q", path, k)
				}
				at = node[i]
			default:
				t.Fatalf("%s: nothing at %q", path, k)
			}
		}
		obj, ok := at.(map[string]any)
		last := keys[len(keys)-1]
		if _, has := obj[last]; !ok || !has {
			t.Fatalf("%s: the answer has no such field", path)
		}
		edit(obj, last)
	}
	var out bytes.Buffer
	if err := json.NewEncoder(&out).Encode(doc); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// served reads a reader that takes its base URL and a bearer key.
func served(path, key string, read func(ctx context.Context, base string) (usage.Reading, error)) func(*testing.T, string) (usage.Reading, error) {
	return func(t *testing.T, body string) (usage.Reading, error) {
		srv := serve(t, path, key, body)
		defer srv.Close()
		return read(context.Background(), srv.URL)
	}
}

// A figure the vendor left out is not a reading of zero. The answer with
// one figure taken out gives the same reading without the window that
// figure made, or the same reading when another field says the same, or
// an error when the figure is all the reader reads. It never gives 0%
// used, a zero balance or a limit reached.
func TestAFigureLeftOutIsNotAReading(t *testing.T) {
	// A budget without its share used is no budget, and a pool with
	// neither a ratio nor its tokens has no share.
	if b := mistralBudgetOf(map[string]any{"initial_budget": 10.0, "currency": "EUR"}); b != nil {
		t.Errorf("a Mistral budget without its share used read as %+v", *b)
	}
	if b := mistralBudgetOf(map[string]any{"usage_percentage": 42.5, "initial_budget": 10.0, "currency": "EUR"}); b == nil || b.UsagePercentage != 42.5 {
		t.Errorf("a whole Mistral budget read as %+v", b)
	}
	for _, p := range []factoryPool{{Allowance: number{200, true}}, {UserTokens: number{50, true}}, {}} {
		if got, ok := p.percent(); ok {
			t.Errorf("a Factory pool %+v without a figure read as %v%% used", p, got)
		}
	}

	ctx := context.Background()
	gateway := func(health, healthBody, budget, header, prefix string, read func(base string) (usage.Reading, error)) func(*testing.T, string) (usage.Reading, error) {
		return func(t *testing.T, body string) (usage.Reading, error) {
			var f fakeGateway
			srv := f.server(t, health, healthBody, budget, header, prefix, body)
			defer srv.Close()
			return read(srv.URL)
		}
	}
	notionNow := func() time.Time { return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC) }
	web := func(session string, read func(base string) (usage.Reading, error)) func(*testing.T, string) (usage.Reading, error) {
		return func(t *testing.T, body string) (usage.Reading, error) {
			return read(newWebServer(t, session, http.StatusUnauthorized, routesIn(t, body)).URL)
		}
	}
	const fuel = `{"code":0,"data":{"totalQuota":1000000,"list":[{"availableToken":400000}]}}`
	readers := map[string]struct {
		// answer is a recorded answer's name, or an answer itself.
		answer string
		read   func(*testing.T, string) (usage.Reading, error)
	}{
		"deepseek": {"deepseek", served("/user/balance", "k", func(ctx context.Context, base string) (usage.Reading, error) {
			return DeepSeek{BaseURL: base}.Read(ctx, "k")
		})},
		"moonshot": {"moonshot", served("/v1/users/me/balance", "k", func(ctx context.Context, base string) (usage.Reading, error) {
			return Moonshot{BaseURL: base}.Read(ctx, "k")
		})},
		"openrouter": {"openrouter", served("/api/v1/key", "k", func(ctx context.Context, base string) (usage.Reading, error) {
			return OpenRouter{BaseURL: base}.Read(ctx, "k")
		})},
		"devpass": {"devpass", served("/v1/key", "k", func(ctx context.Context, base string) (usage.Reading, error) {
			return DevPass{BaseURL: base}.Read(ctx, "k")
		})},
		"devpass pay-as-you-go": {`{"data":{"usage":"12.50","limit":"10","devPlan":"none"}}`, served("/v1/key", "k", func(ctx context.Context, base string) (usage.Reading, error) {
			return DevPass{BaseURL: base}.Read(ctx, "k")
		})},
		"kimi": {"kimi-legacy", served("/coding/v1/usages", "k", func(ctx context.Context, base string) (usage.Reading, error) {
			return Kimi{BaseURL: base}.Read(ctx, "k")
		})},
		"synthetic": {"synthetic", served("/v2/quotas", "k", func(ctx context.Context, base string) (usage.Reading, error) {
			return Synthetic{BaseURL: base}.Read(ctx, "k")
		})},
		"chutes": {"chutes", served("/users/me/subscription_usage", "k", func(ctx context.Context, base string) (usage.Reading, error) {
			return Chutes{BaseURL: base}.Read(ctx, "k")
		})},
		"xkiro": {"xkiro", served("/v1/usage", "k", func(ctx context.Context, base string) (usage.Reading, error) {
			return XKiro{BaseURL: base, Now: xkiroNow}.Read(ctx, "k")
		})},
		"kilo": {"kilo", served(kiloPath, "k", func(ctx context.Context, base string) (usage.Reading, error) {
			return Kilo{BaseURL: base}.Read(ctx, "k")
		})},
		"huggingface": {"huggingface", func(t *testing.T, body string) (usage.Reading, error) {
			srv := hfServer(t, "k", body)
			defer srv.Close()
			return HuggingFace{BaseURL: srv.URL, Now: hfNow}.Read(ctx, "k")
		}},
		"warp": {"warp", func(t *testing.T, body string) (usage.Reading, error) {
			return Warp{BaseURL: warpServer(t, body).URL}.Read(ctx, "wk")
		}},
		"zoommate": {"zoommate", func(t *testing.T, body string) (usage.Reading, error) {
			return ZoomMate{BaseURL: zoomMateServer(t, body).URL}.Read(ctx, "zs=ok")
		}},
		"manus": {"manus", func(t *testing.T, body string) (usage.Reading, error) {
			return Manus{BaseURL: manusServer(t, body).URL}.Read(ctx, "sess")
		}},
		"notion": {"notion", func(t *testing.T, body string) (usage.Reading, error) {
			var asked string
			return Notion{BaseURL: notionServer(t, body, &asked).URL, Now: notionNow}.Read(ctx, "tv2")
		}},
		"helmcode": {"helmcode", func(t *testing.T, body string) (usage.Reading, error) {
			return Helmcode{BaseURL: helmcodeServer(t, body, "", "").URL}.Read(ctx, "hc=ok")
		}},
		"t3chat": {"t3chat", func(t *testing.T, body string) (usage.Reading, error) {
			srv := t3ChatServer(t, http.StatusOK, `{"json":{"0":[[0],[null,0,0]]}}`+"\n"+body)
			return T3Chat{BaseURL: srv.URL}.Read(ctx, "Cookie: sid=ok; theme=dark")
		}},
		"bifrost": {"bifrost", gateway("/health", `{"status":"ok","components":{"db_pings":"ok"}}`, "/api/governance/virtual-keys/quota", "x-bf-vk", "",
			func(base string) (usage.Reading, error) { return Bifrost{}.Read(ctx, base, "vk") })},
		"sub2api": {"sub2api", gateway("/setup/status", `{"code":0,"data":{"needs_setup":false,"step":"completed"}}`, "/v1/usage", "Authorization", "Bearer ",
			func(base string) (usage.Reading, error) { return Sub2API{}.Read(ctx, base, "vk") })},
		"sub2api subscription": {`{"mode":"unrestricted","isValid":true,"planName":"Claude","unit":"USD","remaining":3,
			"subscription":{"daily_usage_usd":2,"monthly_usage_usd":30,"daily_limit_usd":10,"monthly_limit_usd":120}}`,
			gateway("/health", `{"status":"ok"}`, "/v1/usage", "Authorization", "Bearer ",
				func(base string) (usage.Reading, error) { return Sub2API{}.Read(ctx, base, "vk") })},
		"factory allowances": {`{"usage":{"startDate":1700000000000,"endDate":1700003600000,
			"standard":{"userTokens":100,"totalAllowance":1000,"usedRatio":0.10},
			"premium":{"userTokens":10,"totalAllowance":100,"usedRatio":0.10}},"userId":"user-1"}`, func(t *testing.T, body string) (usage.Reading, error) {
			routes := routesOf(t, "factory")
			routes["/api/billing/limits"] = `{"usesTokenRateLimitsBilling":false}`
			routes["/api/organization/subscription/usage"] = body
			srv := factoryServer(t, routes, nil)
			return Factory{API: srv.URL, App: srv.URL, Now: factoryNow}.Read(ctx, "fk-good")
		}},
		"doubao agent plan": {`{"Result":{"AFPFiveHour":{"Quota":200,"Used":50,"ResetTime":1782226478000},"AFPWeekly":{"Quota":400,"Used":100}}}`, func(t *testing.T, body string) (usage.Reading, error) {
			srv := doubaoServer(t, map[string]string{"GetCodingPlanUsage": `{"Result":{"Status":"Expired"}}`, "GetAFPUsage": body})
			return Doubao{BaseURL: srv.URL, Now: doubaoNow}.Read(ctx, "AK:SK:cn-beijing")
		}},
		"aixy": {"aixy", gateway("/none", "", "/v1/usage", "Authorization", "Bearer ",
			func(base string) (usage.Reading, error) { return Aixy{}.Read(ctx, base, "vk") })},
		"commandcode": {"commandcode", web(ccCookie, func(base string) (usage.Reading, error) {
			return CommandCode{BaseURL: base}.Read(ctx, "tok")
		})},
		"mimo": {"mimo", web("api-platform_serviceToken=svc", func(base string) (usage.Reading, error) {
			return MiMo{BaseURL: base}.Read(ctx, mimoSession)
		})},
		"ibmbob": {"ibmbob", func(t *testing.T, body string) (usage.Reading, error) {
			var f struct {
				Profile json.RawMessage            `json:"profile"`
				Budgets map[string]json.RawMessage `json:"budgets"`
			}
			if err := json.Unmarshal([]byte(body), &f); err != nil {
				t.Fatal(err)
			}
			return IBMBob{BaseURL: ibmBobServer(t, "Apikey bk", f.Profile, f.Budgets).URL}.Read(ctx, "bk")
		}},
		"kiro overage": {"kiro-overage", func(t *testing.T, body string) (usage.Reading, error) {
			srv := headerServer(t, http.MethodPost, "/", map[string]string{"Authorization": "Bearer kt"}, body)
			return KiroOverage{BaseURL: srv.URL}.ReadAppLogin(ctx, kiroToken("kt", kiroARN))
		}},
		"longcat": {"longcat", func(t *testing.T, body string) (usage.Reading, error) {
			return LongCat{BaseURL: longCatServer(t, longCatUser, body, "", fuel).URL}.Read(ctx, "lc=ok")
		}},
		"longcat legacy": {`{"code":0,"data":{"usage":{"totalToken":"2000","usedToken":"500","availableToken":"1500"}}}`, func(t *testing.T, body string) (usage.Reading, error) {
			srv := longCatServer(t, longCatUser, `{"code":0,"data":{"currentLot":null}}`, body, fuel)
			return LongCat{BaseURL: srv.URL}.Read(ctx, "lc=ok")
		}},
	}
	const t3 = "json.2.0.0.result.data."
	tests := []struct {
		reader string
		// drop is the fields left out, null the fields sent as null, junk
		// the fields sent as text that is no number.
		drop []string
		null []string
		junk []string
		// loses is the windows the reading is without, less what else of
		// the whole reading the figure carried; the rest of it is the
		// whole answer's. wantErr is the reader refusing the answer.
		loses   int
		less    func(*usage.Reading)
		wantErr bool
	}{
		{reader: "deepseek", drop: []string{"is_available"}},
		{reader: "deepseek", drop: []string{"is_available", "balance_infos"}, wantErr: true},
		{reader: "moonshot", drop: []string{"data.available_balance"}, wantErr: true},
		{reader: "openrouter", drop: []string{"data.usage_monthly"}, wantErr: true},
		{reader: "devpass", drop: []string{"data.devPlanCreditsUsed"}, loses: 1},
		{reader: "devpass", drop: []string{"data.devPlanPremiumCreditsUsed"}, loses: 1},
		{reader: "devpass pay-as-you-go", drop: []string{"data.usage"}, less: func(r *usage.Reading) {
			r.UsedUSD, r.HasUsed, r.LimitReached = 0, false, false
		}},
		{reader: "kimi", drop: []string{"usage.used"}},
		{reader: "kimi", drop: []string{"usage.used", "usage.remaining"}, loses: 1},
		{reader: "kimi", drop: []string{"limits.0.detail.used", "limits.0.detail.remaining"}, loses: 1},
		{reader: "synthetic", drop: []string{"subscription.requests"}, loses: 1},
		{reader: "chutes", drop: []string{"four_hour.usage"}, loses: 1},
		{reader: "xkiro", drop: []string{"windows.0.spent_usd"}, loses: 1},
		{reader: "kilo", drop: []string{"1.result.data.json.subscription.currentPeriodUsageUsd"}, loses: 1},
		{reader: "huggingface", drop: []string{"usage.inferenceProviders.usedNanoUsd"}, wantErr: true},
		{reader: "huggingface", drop: []string{"usage.inferenceProviders.includedNanoUsd"}, wantErr: true},
		{reader: "warp", drop: []string{"data.user.user.requestLimitInfo.requestsUsedSinceLastRefresh"}, loses: 1},
		{reader: "zoommate", drop: []string{"data.credit_status.used_credit"}, wantErr: true},
		{reader: "zoommate", null: []string{"data.credit_status.used_credit"}, wantErr: true},
		// The credits left are the cap less what is used.
		{reader: "zoommate", junk: []string{"data.credit_status.used_credit"}, loses: 1, less: func(r *usage.Reading) {
			r.Credits, r.CreditsUnit, r.HasCredits = 0, "", false
		}},
		{reader: "manus", drop: []string{"proMonthlyCredits"}, loses: 1},
		{reader: "manus", drop: []string{"periodicCredits"}, loses: 1},
		{reader: "manus", drop: []string{"maxRefreshCredits"}, loses: 1},
		{reader: "manus", drop: []string{"refreshCredits"}, loses: 1},
		{reader: "notion", drop: []string{"window.used"}, loses: 1},
		{reader: "notion", drop: []string{"billingPeriodWindow.used"}, loses: 1},
		{reader: "notion", null: []string{"window.used"}, loses: 1},
		{reader: "notion", junk: []string{"window.used"}, loses: 1},
		{reader: "notion", junk: []string{"billingPeriodWindow.used"}, loses: 1},
		{reader: "helmcode", drop: []string{"models.0.tokensUsed"}, loses: 1},
		{reader: "t3chat", drop: []string{t3 + "usageFourHourPercentage"}, loses: 1},
		{reader: "t3chat", drop: []string{t3 + "usageMonthPercentage"}, loses: 1},
		{reader: "t3chat", drop: []string{t3 + "usageFourHourPercentage", t3 + "usageMonthPercentage"}, wantErr: true},
		{reader: "bifrost", drop: []string{"is_active"}},
		{reader: "bifrost", drop: []string{"budgets.0.current_usage"}, loses: 1},
		{reader: "sub2api", drop: []string{"rate_limits.0.used"}, loses: 1},
		{reader: "sub2api", null: []string{"rate_limits.1.used"}, loses: 1},
		{reader: "sub2api", drop: []string{"quota.used"}, less: func(r *usage.Reading) {
			r.UsedUSD, r.HasUsed, r.LimitUSD = 0, false, 0
		}},
		{reader: "sub2api subscription", drop: []string{"subscription.daily_usage_usd"}, loses: 1},
		{reader: "factory allowances", drop: []string{"usage.premium.usedRatio", "usage.premium.userTokens"}, loses: 1},
		{reader: "factory allowances", drop: []string{"usage.premium.usedRatio", "usage.premium.totalAllowance",
			"usage.standard.usedRatio", "usage.standard.userTokens"}, wantErr: true},
		{reader: "doubao agent plan", drop: []string{"Result.AFPFiveHour.Used"}, loses: 1},
		{reader: "aixy", drop: []string{"budgets.0.spend_usd"}, loses: 1},
		// The hard budget unknown, the monitoring one is the spend against a cap.
		{reader: "aixy", drop: []string{"budgets.1.availability.spent_usd"}, loses: 1, less: func(r *usage.Reading) {
			r.UsedUSD, r.LimitUSD = 900, 1000
		}},
		{reader: "commandcode", drop: []string{"/internal/billing/credits.windowLimits.fiveHour.used"}, loses: 1},
		{reader: "mimo", drop: []string{"/api/v1/tokenPlan/usage.data.monthUsage.items.0.percent"}},
		{reader: "mimo", drop: []string{"/api/v1/tokenPlan/usage.data.monthUsage.items.0.percent", "/api/v1/tokenPlan/usage.data.monthUsage.items.0.used"}, loses: 1},
		{reader: "ibmbob", drop: []string{"budgets.team-a.usage"}, loses: 1},
		{reader: "kiro overage", drop: []string{"usageBreakdownList.0.currentOveragesWithPrecision"}, loses: 1},
		{reader: "longcat", drop: []string{"data.currentLot.consumedToken"}, wantErr: true},
		{reader: "longcat legacy", drop: []string{"data.usage.usedToken"}},
		{reader: "longcat legacy", drop: []string{"data.usage.usedToken", "data.usage.availableToken"}, loses: 1, less: func(r *usage.Reading) {
			r.Credits, r.CreditsUnit, r.HasCredits = 0, "", false
		}},
	}
	for _, tt := range tests {
		t.Run(tt.reader+" without "+strings.Join(append(append(tt.drop, tt.null...), tt.junk...), ", "), func(t *testing.T) {
			r := readers[tt.reader]
			whole := r.answer
			if !strings.HasPrefix(whole, "{") {
				whole = fixture(t, r.answer)
			}
			full, err := r.read(t, whole)
			if err != nil {
				t.Fatalf("the whole answer: %v", err)
			}
			got, err := r.read(t, garbled(t, nulled(t, without(t, whole, tt.drop...), tt.null...), tt.junk...))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("read as %+v, want the answer refused", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Windows) != len(full.Windows)-tt.loses {
				t.Fatalf("windows %+v, want %d fewer than %+v", got.Windows, tt.loses, full.Windows)
			}
			for _, w := range got.Windows {
				kept := false
				for _, f := range full.Windows {
					// A share worked out from the tokens differs from the
					// vendor's rounded one in its last digits.
					if approx(w.UsedPct, f.UsedPct) {
						f.UsedPct = w.UsedPct
					}
					kept = kept || reflect.DeepEqual(w, f)
				}
				if !kept {
					t.Errorf("window %+v is not one the whole answer gives: %+v", w, full.Windows)
				}
			}
			if len(got.Windows) == 0 {
				// A reading left with no window is not shown as a plan.
				full.Subscription = got.Subscription
			}
			if tt.less != nil {
				tt.less(&full)
			}
			got.Windows, full.Windows = nil, nil
			if !reflect.DeepEqual(got, full) {
				t.Errorf("the rest of the reading changed:\n got %+v\nwant %+v", got, full)
			}
		})
	}
}
