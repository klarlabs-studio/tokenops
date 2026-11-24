package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerTraeCN registers the reader (readers_gen.go).
func readerTraeCN() usage.Reader { return TraeCN{} }

// TraeCN reads the credits ByteDance's China-region Trae (trae.cn) has
// used, ported from CodexBar (Sources/CodexBarCore/Resources/Plugins/
// traecn.js, Providers/TraeCN, docs/traecn.md): the dashboard's browser
// session is exchanged for a short-lived token (POST /cloudide/api/v3/
// common/GetUserToken), which reads the entitlement summary (POST
// /trae/api/v2/pay/user_current_entitlement_list). The token is used for
// that one read and never kept. The console API is reverse engineered.
//
// Credits used across all packs against their total is the "credits"
// window. Packs expire on their own, so no reset is claimed.
type TraeCN struct {
	BaseURL string
	HTTP    *http.Client
}

func (TraeCN) Endpoint() string               { return "trae-cn" }
func (TraeCN) Provider() eventschema.Provider { return "trae-cn" }
func (TraeCN) Source() string                 { return "trae-cn-web" }

var errTraeShape = errors.New("accounts: trae cn entitlements: unrecognised answer")

func (t TraeCN) Read(ctx context.Context, key string) (usage.Reading, error) {
	cookie := cookieHeader(key)
	if !isSession(cookie) {
		return usage.Reading{}, fmt.Errorf("%w (the Trae CN credential is not a Cookie header)", usage.ErrAuth)
	}
	api := base(t.BaseURL, "https://api.trae.cn")
	header := http.Header{
		"Cookie": {cookie}, "Origin": {"https://www.trae.cn"}, "Referer": {"https://www.trae.cn/"},
		"Content-Type": {"application/json"}, "Accept": {"application/json"},
	}
	body, err := doWeb(ctx, t.HTTP, http.MethodPost, api+"/cloudide/api/v3/common/GetUserToken", header, []byte(`{}`))
	if err != nil {
		return usage.Reading{}, err
	}
	var tok struct {
		Result *struct {
			Token string `json:"Token"`
		} `json:"Result"`
	}
	if json.Unmarshal(body, &tok) != nil || tok.Result == nil || tok.Result.Token == "" {
		return usage.Reading{}, errTraeShape
	}
	h := header.Clone()
	h.Set("Authorization", "Cloud-IDE-JWT "+tok.Result.Token)
	body, err = doWeb(ctx, t.HTTP, http.MethodPost, api+"/trae/api/v2/pay/user_current_entitlement_list", h,
		[]byte(`{"require_usage":true,"full_data":true}`))
	if err != nil {
		return usage.Reading{}, err
	}
	var resp struct {
		Code    *int  `json:"code"`
		Credits *bool `json:"is_credits_billing"`
		Summary *struct {
			Total    *float64 `json:"total_amount"`
			Consumed *float64 `json:"consumed_amount"`
		} `json:"usage_summary"`
	}
	if json.Unmarshal(body, &resp) != nil {
		return usage.Reading{}, errTraeShape
	}
	if resp.Code != nil && *resp.Code == 1001 {
		return usage.Reading{}, fmt.Errorf("%w (Trae CN: signed out)", usage.ErrAuth)
	}
	s := resp.Summary
	if resp.Credits == nil || !*resp.Credits || s == nil || s.Total == nil || s.Consumed == nil || *s.Total < 0 || *s.Consumed < 0 {
		return usage.Reading{}, errTraeShape
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	if *s.Total > 0 {
		r.Windows = []usage.Window{{Name: "credits", UsedPct: clampPct(pct(*s.Consumed, *s.Total))}}
	}
	return r, nil
}
