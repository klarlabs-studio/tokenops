package accounts

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// typeSafeConsole is the console for the cookie "ts=ok": the billing page
// names one script chunk, the chunk holds the current action ID (*current),
// and the action answers with result.
type typeSafeConsole struct {
	current    atomic.Value
	pageLoads  atomic.Int32
	result     string
	loginLand  bool
	sawSession atomic.Bool
}

func (c *typeSafeConsole) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_next/static/chunks/billing.js" {
			if r.Header.Get("Cookie") != "" {
				c.sawSession.Store(true)
			}
			_, _ = w.Write([]byte(`createServerReference("` + c.current.Load().(string) + `",callServer,void 0,findSourceMapURL,"getBillingOverviewResult")`))
			return
		}
		if r.URL.Path != "/settings/billing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Cookie") != "ts=ok" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodGet {
			c.pageLoads.Add(1)
			if c.loginLand {
				_, _ = w.Write([]byte(`<html><script>self.__next_f.push([1,"[\"(auth)\",{\"children\":[\"login\""])</script></html>`))
				return
			}
			_, _ = w.Write([]byte(`<html><script src="/_next/static/chunks/billing.js" async></script><script src="https://cdn.example/x.js"></script></html>`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Next-Action") != c.current.Load().(string) {
			w.Header().Set("X-Nextjs-Action-Not-Found", "1")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if string(body) != "[]" || r.Header.Get("Accept") != "text/x-component" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("0:{\"a\":\"$@1\",\"f\":\"\",\"b\":\"x\"}\n1:" + c.result + "\n"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const typeSafeID1 = "7f00aa11bb22cc33dd44ee55ff66778899aabbcc01"

func TestTypeSafeReadsTheBilling(t *testing.T) {
	c := &typeSafeConsole{result: strings.TrimSpace(fixture(t, "typesafe"))}
	c.current.Store(typeSafeID1)
	srv := c.server(t)
	got, err := TypeSafe{BaseURL: srv.URL}.Read(context.Background(), "Cookie: ts=ok")
	if err != nil || !got.HasUsed || !approx(got.UsedUSD, 18.42) || !got.HasBalance || !approx(got.BalanceUSD, 31.58) || got.Subscription {
		t.Fatalf("%+v, %v", got, err)
	}
	// The action ID is kept: a second read loads no page.
	if _, err := (TypeSafe{BaseURL: srv.URL}).Read(context.Background(), "ts=ok"); err != nil || c.pageLoads.Load() != 1 {
		t.Errorf("second read %v after %d page loads", err, c.pageLoads.Load())
	}
	if c.sawSession.Load() {
		t.Error("the session was sent with a static chunk")
	}
	// A new deploy: the stale ID is found again, once.
	c.current.Store("aa00aa11bb22cc33dd44ee55ff66778899aabbcc02")
	if got, err := (TypeSafe{BaseURL: srv.URL}).Read(context.Background(), "ts=ok"); err != nil || !approx(got.BalanceUSD, 31.58) || c.pageLoads.Load() != 2 {
		t.Errorf("after a deploy %+v, %v, %d page loads", got, err, c.pageLoads.Load())
	}
}

func TestTypeSafeRefusals(t *testing.T) {
	c := &typeSafeConsole{result: strings.TrimSpace(fixture(t, "typesafe"))}
	c.current.Store(typeSafeID1)
	srv := c.server(t)
	for _, cred := range []string{"ts=expired", "token"} {
		if _, err := (TypeSafe{BaseURL: srv.URL}).Read(context.Background(), cred); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q = %v, want ErrAuth", cred, err)
		}
	}
	login := &typeSafeConsole{loginLand: true}
	login.current.Store(typeSafeID1)
	if _, err := (TypeSafe{BaseURL: login.server(t).URL}).Read(context.Background(), "ts=ok"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("sign-in page = %v, want ErrAuth", err)
	}
}

func TestTypeSafeUnknownShape(t *testing.T) {
	for _, result := range []string{`{"ok":false}`, `{"ok":true,"data":{"billing":{"plan":"free_plan"}}}`, `{"x":1}`} {
		c := &typeSafeConsole{result: result}
		c.current.Store(typeSafeID1)
		if got, err := (TypeSafe{BaseURL: c.server(t).URL}).Read(context.Background(), "ts=ok"); err == nil || errors.Is(err, usage.ErrAuth) {
			t.Errorf("%s = %+v, %v", result, got, err)
		}
	}
}
