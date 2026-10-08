package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerXAPI registers the reader (readers_gen.go).
func readerXAPI() usage.Reader { return XAPI{} }

// XAPI reads the X API's prepaid credit (purchased plus free) from the X
// developer console, console.x.com, with its browser session, as
// CodexBar's X API provider does
// (Sources/CodexBarCore/Resources/Plugins/xapi.js, docs/xapi.md): /api/me
// names the account, /api/accounts/{id}/credits holds both balances, in
// dollars. A balance below zero is kept: the console lets it go negative.
type XAPI struct {
	BaseURL string
	HTTP    *http.Client
}

func (XAPI) Endpoint() string               { return "xapi" }
func (XAPI) Provider() eventschema.Provider { return "xapi" }
func (XAPI) Source() string                 { return "xapi-web" }

var xapiAccountID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (x XAPI) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	cookie = cookieHeader(cookie)
	csrf := cookieValue(cookie, "ct0")
	if !isSession(cookie) || cookieValue(cookie, "auth_token") == "" || csrf == "" || strings.ContainsAny(csrf, "\r\n") {
		return usage.Reading{}, fmt.Errorf("%w (the X API console is read with its auth_token and ct0 cookies)", usage.ErrAuth)
	}
	root := base(x.BaseURL, "https://console.x.com")
	header := http.Header{"Cookie": {cookie}, "X-Csrf-Token": {csrf}, "Accept": {"application/json"}}
	var me struct {
		Account *struct {
			ID json.RawMessage `json:"id"`
		} `json:"account"`
	}
	if err := xapiGet(ctx, x.HTTP, root+"/api/me", header, &me); err != nil {
		return usage.Reading{}, err
	}
	id := ""
	if me.Account != nil {
		id = xapiID(me.Account.ID)
	}
	if id == "" {
		return usage.Reading{}, errors.New("accounts: GET console.x.com/api/me: no account")
	}
	var credits struct {
		Credits *struct {
			Balance json.RawMessage `json:"balance"`
		} `json:"credits"`
		Free *struct {
			Balance json.RawMessage `json:"balance"`
		} `json:"freeCredits"`
	}
	if err := xapiGet(ctx, x.HTTP, root+"/api/accounts/"+id+"/credits", header, &credits); err != nil {
		return usage.Reading{}, err
	}
	if credits.Credits == nil {
		return usage.Reading{}, errors.New("accounts: GET console.x.com credits: no balance")
	}
	paid, ok := finiteNumber(credits.Credits.Balance)
	free := 0.0
	if credits.Free != nil {
		var okFree bool
		if free, okFree = finiteNumber(credits.Free.Balance); !okFree {
			ok = false
		}
	}
	if !ok {
		return usage.Reading{}, errors.New("accounts: GET console.x.com credits: unreadable balance")
	}
	return usage.Reading{Scope: "account", BalanceUSD: paid + free, HasBalance: true}, nil
}

// xapiGet reads one console answer. The console answers a signed-out
// request with 400 and error code 215, which is a refusal like 401.
func xapiGet(ctx context.Context, hc *http.Client, u string, header http.Header, out any) error {
	body, err := doWeb(ctx, hc, http.MethodGet, u, header, nil)
	if err != nil {
		var se *statusError
		if errors.As(err, &se) && se.status == http.StatusBadRequest && xapiSignedOut(se.body) {
			return fmt.Errorf("%w (signed out on %s)", usage.ErrAuth, hostPath(u))
		}
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("accounts: GET %s: %w", hostPath(u), err)
	}
	return nil
}

func xapiSignedOut(body []byte) bool {
	var e struct {
		Errors []struct {
			Code int `json:"code"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &e) != nil {
		return false
	}
	for _, x := range e.Errors {
		if x.Code == 215 {
			return true
		}
	}
	return false
}

// xapiID is the account ID, a string or a whole number, when it is safe
// to put in a path.
func xapiID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		var n float64
		if json.Unmarshal(raw, &n) != nil || n < 0 || n > 1<<53 || n != math.Trunc(n) {
			return ""
		}
		s = strconv.FormatInt(int64(n), 10)
	}
	if !xapiAccountID.MatchString(s) {
		return ""
	}
	return s
}

// finiteNumber is a finite JSON number; a string or null is not one.
func finiteNumber(raw json.RawMessage) (float64, bool) {
	var v float64
	if len(raw) == 0 || raw[0] == '"' || string(raw) == "null" || json.Unmarshal(raw, &v) != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, false
	}
	return v, true
}
