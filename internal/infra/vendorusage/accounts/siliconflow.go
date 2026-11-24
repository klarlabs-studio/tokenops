package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerSiliconFlow and readerSiliconFlowWeb register the readers
// (readers_gen.go).
func readerSiliconFlow() usage.Reader    { return SiliconFlow{} }
func readerSiliconFlowWeb() usage.Reader { return SiliconFlowWeb{} }

// SiliconFlow reads an international account's balance with its API key,
// ported from CodexBar (Sources/CodexBarCore/Resources/Plugins/
// siliconflow.ts, Providers/SiliconFlow, docs/siliconflow.md): GET
// api.siliconflow.com/v1/user/info (docs.siliconflow.com, "Retrieve user
// info"), whose totalBalance is USD. The China origin's balance API was
// retired on 2026-08-14; a China account is read with SiliconFlowWeb.
type SiliconFlow struct {
	BaseURL string
	HTTP    *http.Client
}

func (SiliconFlow) Endpoint() string               { return "siliconflow" }
func (SiliconFlow) Provider() eventschema.Provider { return "siliconflow" }
func (SiliconFlow) Source() string                 { return "siliconflow-account" }
func (SiliconFlow) KeyOnly() bool                  { return true }

// siliconFlowAmount is a decimal amount as SiliconFlow writes it.
var siliconFlowAmount = lazyRegexp(`^-?(?:\d+|\d+\.\d+)$`)

func (s SiliconFlow) Read(ctx context.Context, key string) (usage.Reading, error) {
	if isSession(key) {
		return usage.Reading{}, usage.ErrSkip
	}
	var resp struct {
		Code   *int  `json:"code"`
		Status *bool `json:"status"`
		Data   *struct {
			TotalBalance json.RawMessage `json:"totalBalance"`
			Status       string          `json:"status"`
		} `json:"data"`
	}
	err := getJSON(ctx, s.HTTP, base(s.BaseURL, "https://api.siliconflow.com")+"/v1/user/info", key, &resp)
	var se *statusError
	if errors.As(err, &se) && se.status == http.StatusGone {
		return usage.Reading{}, errors.New("accounts: siliconflow: the balance query was refused (check the balance in the SiliconFlow console)")
	}
	if err != nil {
		return usage.Reading{}, err
	}
	switch {
	case resp.Code == nil:
		return usage.Reading{}, errors.New("accounts: siliconflow user info: unrecognised answer")
	case *resp.Code == 30014:
		return usage.Reading{}, fmt.Errorf("%w (SiliconFlow code 30014)", usage.ErrAuth)
	case *resp.Code == 20092:
		return usage.Reading{}, errors.New("accounts: siliconflow: the balance query was refused (check the balance in the SiliconFlow console)")
	case *resp.Code != 20000 || resp.Status == nil || !*resp.Status || resp.Data == nil:
		return usage.Reading{}, errors.New("accounts: siliconflow user info: unrecognised answer")
	}
	total, ok := siliconFlowMoney(resp.Data.TotalBalance)
	if !ok {
		return usage.Reading{}, errors.New("accounts: siliconflow user info: unrecognised balance")
	}
	// A negative total is kept: an account can be overdue.
	return usage.Reading{Scope: "account", BalanceUSD: total, HasBalance: true}, nil
}

// siliconFlowMoney reads a decimal string or a number.
func siliconFlowMoney(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return f, true
	}
	var s string
	if json.Unmarshal(raw, &s) != nil || !siliconFlowAmount().MatchString(s) {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

// SiliconFlowWeb reads a China account's console balance with the
// browser session at cloud.siliconflow.cn, ported from CodexBar
// (Sources/CodexBarCore/Resources/Plugins/siliconflow-web.js): the billing
// page answers the active subject in its x-subject-id header, and the
// wallet endpoint reads that subject's balance in 10^-12 CNY. The balance
// is kept in CNY, never converted. Organisation subjects and vouchers are
// not read.
type SiliconFlowWeb struct {
	BaseURL string
	HTTP    *http.Client
}

func (SiliconFlowWeb) Endpoint() string               { return "siliconflow" }
func (SiliconFlowWeb) Provider() eventschema.Provider { return "siliconflow" }
func (SiliconFlowWeb) Source() string                 { return "siliconflow-web" }

var (
	siliconFlowSubject = lazyRegexp(`^[A-Za-z0-9_-]{1,128}$`)
	siliconFlowYuan    = lazyRegexp(`^(-?)(\d{1,30})$`)
)

func (s SiliconFlowWeb) Read(ctx context.Context, key string) (usage.Reading, error) {
	cookie := cookieHeader(key)
	if !isSession(cookie) {
		return usage.Reading{}, usage.ErrSkip
	}
	origin := base(s.BaseURL, "https://cloud.siliconflow.cn")
	page := origin + "/me/expensebill"
	// The page body is not read; the active subject is in a header. A
	// redirect to account.siliconflow.cn/login is a signed-out session.
	resp, err := doWebResponse(ctx, s.HTTP, http.MethodGet, page, http.Header{"Accept": {"text/html"}, "Cookie": {cookie}}, nil)
	if err != nil {
		return usage.Reading{}, err
	}
	subject := resp.header.Get("x-subject-id")
	if !siliconFlowSubject().MatchString(subject) {
		return usage.Reading{}, errors.New("accounts: siliconflow console: no subject in the answer")
	}
	body, err := doWeb(ctx, s.HTTP, http.MethodGet, origin+"/walletd-server/api/v1/subject/profile/peek", http.Header{
		"Accept": {"application/json"}, "Cookie": {cookie}, "Referer": {page}, "x-subject-id": {subject},
	}, nil)
	if err != nil {
		return usage.Reading{}, err
	}
	var wallet struct {
		Code *int `json:"code"`
		Data *struct {
			FinancialInfo *struct {
				Balance json.RawMessage `json:"balance"`
			} `json:"financialInfo"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &wallet) != nil || wallet.Code == nil || *wallet.Code != 20000 ||
		wallet.Data == nil || wallet.Data.FinancialInfo == nil {
		return usage.Reading{}, errors.New("accounts: siliconflow wallet: unrecognised answer")
	}
	yuan, ok := siliconFlowPicoYuan(wallet.Data.FinancialInfo.Balance)
	if !ok {
		return usage.Reading{}, errors.New("accounts: siliconflow wallet: unrecognised balance")
	}
	return usage.Reading{Scope: "account", Credits: yuan, CreditsUnit: "CNY", HasCredits: true}, nil
}

// siliconFlowPicoYuan reads an integer string of 10^-12 CNY into yuan,
// splitting the digits rather than dividing a lossy number.
func siliconFlowPicoYuan(raw json.RawMessage) (float64, bool) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return 0, false
	}
	m := siliconFlowYuan().FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	digits := strings.Repeat("0", max(0, 13-len(m[2]))) + m[2]
	v, err := strconv.ParseFloat(m[1]+digits[:len(digits)-12]+"."+digits[len(digits)-12:], 64)
	return v, err == nil
}
