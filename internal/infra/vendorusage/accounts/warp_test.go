package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// warpServer answers the GraphQL query for the key "wk", checking the
// request names Warp's client as Warp's edge requires.
func warpServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer wk" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			OperationName string `json:"operationName"`
		}
		if r.Method != http.MethodPost || r.URL.Path != "/graphql/v2" || r.URL.Query().Get("op") != "GetRequestLimitInfo" ||
			r.Header.Get("User-Agent") != "Warp/1.0" || r.Header.Get("X-Warp-Client-Id") != "warp-app" ||
			json.NewDecoder(r.Body).Decode(&req) != nil || req.OperationName != "GetRequestLimitInfo" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWarpReadsTheCredits(t *testing.T) {
	srv := warpServer(t, fixture(t, "warp"))
	got, err := Warp{BaseURL: srv.URL}.Read(context.Background(), "wk")
	if err != nil || !got.Subscription || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "credits" || !approx(w.UsedPct, 40) || !w.ResetsAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("credits %+v", w)
	}
	// The add-on pool, the user's and the workspace's grants together, is
	// a credits balance with no window, never dollars.
	if !got.HasCredits || got.Credits != 500 || got.CreditsUnit != "credits" || got.HasBalance {
		t.Errorf("add-on credits %+v", got)
	}
	a := usage.NewEnvelope(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), Warp{}, got).Attributes
	if a["balance_credits"] != "500" || a["balance_credits_unit"] != "credits" || a["window_0_name"] != "credits" {
		t.Errorf("attributes %v", a)
	}
}

func TestWarpUnlimitedAndUnknown(t *testing.T) {
	srv := warpServer(t, `{"data":{"user":{"__typename":"UserOutput","user":{"requestLimitInfo":{"isUnlimited":true,"requestLimit":0,"requestsUsedSinceLastRefresh":12}}}}}`)
	if got, err := (Warp{BaseURL: srv.URL}).Read(context.Background(), "wk"); err != nil || !got.Empty() {
		t.Errorf("unlimited = %+v, %v", got, err)
	}
	// An unlimited plan's add-on pool is still read.
	pool := warpServer(t, `{"data":{"user":{"__typename":"UserOutput","user":{"requestLimitInfo":{"isUnlimited":true},"bonusGrants":[{"requestCreditsGranted":"100","requestCreditsRemaining":"0"}],"workspaces":[{"bonusGrantsInfo":null}]}}}}`)
	if got, err := (Warp{BaseURL: pool.URL}).Read(context.Background(), "wk"); err != nil || got.Subscription || !got.HasCredits || got.Credits != 0 || got.Empty() {
		t.Errorf("unlimited with a spent pool = %+v, %v", got, err)
	}
	unknown := warpServer(t, `{"data":{"user":{"__typename":"UserFacingError"}}}`)
	if _, err := (Warp{BaseURL: unknown.URL}).Read(context.Background(), "wk"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("an unknown answer = %v", err)
	}
}

func TestWarpRefusesTheKey(t *testing.T) {
	srv := warpServer(t, fixture(t, "warp"))
	if _, err := (Warp{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a refused key = %v, want ErrAuth", err)
	}
	body := warpServer(t, `{"errors":[{"message":"Unauthenticated","extensions":{"code":"UNAUTHENTICATED"}}]}`)
	if _, err := (Warp{BaseURL: body.URL}).Read(context.Background(), "wk"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a refusal in the body = %v, want ErrAuth", err)
	}
}
