package accounts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// jwtWithDevice is an unsigned JWT whose payload names a device.
func jwtWithDevice(device string) string {
	payload, _ := json.Marshal(map[string]string{"device_id": device})
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// stepfunServer is StepFun's platform: it signs in one user, issues one
// token pair, and answers the rate-limit query for that token with body,
// checking the web ID matches the token's device.
func stepfunServer(t *testing.T, body string) (*httptest.Server, string) {
	t.Helper()
	token := jwtWithDevice("dev-anon") + "..." + jwtWithDevice("dev-user")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/":
			http.SetCookie(w, &http.Cookie{Name: "INGRESSCOOKIE", Value: "ing"})
		case "/passport/proto.api.passport.v1.PassportService/RegisterDevice":
			if !strings.Contains(r.Header.Get("Cookie"), "INGRESSCOOKIE=ing") {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"accessToken":{"raw":"` + jwtWithDevice("dev-anon") + `"}}`))
		case "/passport/proto.api.passport.v1.PassportService/SignInByPassword":
			var creds map[string]string
			_ = json.Unmarshal(b, &creds)
			if creds["username"] != "me@example.com" || creds["password"] != "s3cret" || r.Header.Get("Oasis-Webid") != "dev-anon" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			parts := strings.Split(token, "...")
			_, _ = w.Write([]byte(`{"accessToken":{"raw":"` + parts[0] + `"},"refreshToken":{"raw":"` + parts[1] + `"}}`))
		case "/api/step.openapi.devcenter.Dashboard/QueryStepPlanRateLimit":
			if !strings.Contains(r.Header.Get("Cookie"), "Oasis-Token="+token) || r.Header.Get("Oasis-Webid") != "dev-user" {
				_, _ = w.Write([]byte(fixturePart(t, "stepfun", "unauthorized")))
				return
			}
			_, _ = w.Write([]byte(body))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, token
}

func TestStepFunSignsInAndReadsWindows(t *testing.T) {
	srv, want := stepfunServer(t, fixturePart(t, "stepfun", "windows"))
	s := StepFun{BaseURL: srv.URL}
	token, err := s.Login(context.Background(), "me@example.com", "s3cret")
	if err != nil || token != want {
		t.Fatalf("login = %v", err)
	}
	got, err := s.Read(context.Background(), token)
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || !approx(w.UsedPct, 20) || !w.ResetsAt.Equal(time.Unix(1777528800, 0).UTC()) {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 40) {
		t.Errorf("week %+v", w)
	}
	// A pasted cookie header carrying the token reads the same.
	if _, err := s.Read(context.Background(), "Oasis-Token="+token+"; Oasis-Webid=dev-user"); err != nil {
		t.Errorf("cookie header = %v", err)
	}
}

func TestStepFunWrongPasswordNeverEchoed(t *testing.T) {
	srv, _ := stepfunServer(t, "{}")
	_, err := StepFun{BaseURL: srv.URL}.Login(context.Background(), "me@example.com", "wrong-pass")
	if !errors.Is(err, usage.ErrAuth) || strings.Contains(err.Error(), "wrong-pass") {
		t.Errorf("err = %v", err)
	}
}

// A Token Plan's credit pool weighs the buckets' residual over their total.
func TestStepFunCreditPlan(t *testing.T) {
	srv, token := stepfunServer(t, fixturePart(t, "stepfun", "credits"))
	got, err := StepFun{BaseURL: srv.URL}.Read(context.Background(), token)
	if err != nil || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "credits" || !approx(w.UsedPct, 20) || !w.ResetsAt.Equal(time.Unix(1780000000, 0).UTC()) {
		t.Errorf("credits %+v", w)
	}
}

func TestStepFunExpiredTokenAndShape(t *testing.T) {
	srv, _ := stepfunServer(t, "{}")
	if _, err := (StepFun{BaseURL: srv.URL}).Read(context.Background(), "stale-token"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("expired = %v", err)
	}
	odd, token := stepfunServer(t, `{"status":1}`)
	if _, err := (StepFun{BaseURL: odd.URL}).Read(context.Background(), token); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("missing fields = %v", err)
	}
}
