package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// serveRoutes answers each path with its body when header carries want,
// 401 otherwise, and 404 for a path it does not know.
func serveRoutes(t *testing.T, header, want string, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(header) != want {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// deepgramRoutes turns the fixture (the documented projects answer and
// each project's documented balances answer) into routes.
func deepgramRoutes(t *testing.T) map[string]string {
	t.Helper()
	var f struct {
		Projects json.RawMessage            `json:"projects"`
		Balances map[string]json.RawMessage `json:"balances"`
	}
	if err := json.Unmarshal([]byte(fixture(t, "deepgram")), &f); err != nil {
		t.Fatal(err)
	}
	routes := map[string]string{"/v1/projects": string(f.Projects)}
	for id, b := range f.Balances {
		routes["/v1/projects/"+id+"/balances"] = string(b)
	}
	return routes
}

// The USD balances of every project add up; a balance in hours is not
// dollars and is left out.
func TestDeepgramAddsUpUSDBalances(t *testing.T) {
	srv := serveRoutes(t, "Authorization", "Token dg-key", deepgramRoutes(t))
	got, err := Deepgram{BaseURL: srv.URL}.Read(context.Background(), "dg-key")
	if err != nil || !got.HasBalance || !approx(got.BalanceUSD, 1300) || got.HasUsed || got.Subscription {
		t.Errorf("%+v %v", got, err)
	}
}

func TestDeepgramRefusedKey(t *testing.T) {
	srv := serveRoutes(t, "Authorization", "Token dg-key", deepgramRoutes(t))
	if _, err := (Deepgram{BaseURL: srv.URL}).Read(context.Background(), "other"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("err %v", err)
	}
}

// A key with no projects, or projects without a USD balance, reads as
// nothing to store.
func TestDeepgramNoBalance(t *testing.T) {
	srv := serveRoutes(t, "Authorization", "Token k", map[string]string{
		"/v1/projects":             `{"projects":[{"project_id":"p1"}]}`,
		"/v1/projects/p1/balances": `{"balances":[]}`,
	})
	if got, err := (Deepgram{BaseURL: srv.URL}).Read(context.Background(), "k"); err != nil || !got.Empty() {
		t.Errorf("%+v %v", got, err)
	}
	none := serveRoutes(t, "Authorization", "Token k", map[string]string{"/v1/projects": `{}`})
	if got, err := (Deepgram{BaseURL: none.URL}).Read(context.Background(), "k"); err != nil || !got.Empty() {
		t.Errorf("%+v %v", got, err)
	}
}
