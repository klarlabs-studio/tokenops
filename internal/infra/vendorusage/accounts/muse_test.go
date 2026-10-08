package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// museServer answers POST /muse-code/key for the device token
// "dca:tok", checking the API version header Muse Code's CLI sends.
func museServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dca:tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/muse-code/key" || r.Header.Get("X-Api-Version") != "1.0.0" ||
			r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMuseReadsTheQuota(t *testing.T) {
	srv := museServer(t, fixture(t, "muse"))
	got, err := Muse{BaseURL: srv.URL}.Read(context.Background(), "dca:tok")
	if err != nil || !got.Subscription || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || w.UsedPct != 96 || w.Duration != 5*time.Hour || !w.ResetsAt.Equal(time.Unix(1788599502, 0)) {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || w.UsedPct != 40 || w.Duration != 7*24*time.Hour || !w.ResetsAt.Equal(time.Unix(1788739200, 0)) {
		t.Errorf("week %+v", w)
	}
}

func TestMuseIdleOrNoSubscription(t *testing.T) {
	for _, body := range []string{
		`{"require_payment":false,"is_subs_active":true,"subs_tier_name":"Muse Code Power Usage"}`,
		`{"require_payment":false,"is_subs_active":true,"subs_usage":null}`,
		`{"require_payment":true,"is_subs_active":true}`,
		`{"require_payment":false,"is_subs_active":false}`,
	} {
		got, err := Muse{BaseURL: museServer(t, body).URL}.Read(context.Background(), "dca:tok")
		if err != nil || !got.Empty() {
			t.Errorf("%s = %+v, %v; want an empty reading", body, got, err)
		}
	}
	// A reset out of range keeps the share.
	got, err := Muse{BaseURL: museServer(t, `{"is_subs_active":true,"subs_usage":{"window":{"used_percent":120,"window_duration_mins":300,"resets_at":0},"weekly":{"used_percent":-3,"resets_at":null}}}`).URL}.Read(context.Background(), "dca:tok")
	if err != nil || got.Windows[0].UsedPct != 100 || !got.Windows[0].ResetsAt.IsZero() || got.Windows[1].UsedPct != 0 {
		t.Errorf("clamped = %+v, %v", got, err)
	}
}

func TestMuseRefusesTheToken(t *testing.T) {
	srv := museServer(t, fixture(t, "muse"))
	if _, err := (Muse{BaseURL: srv.URL}).Read(context.Background(), "dca:expired"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("an expired login = %v, want ErrAuth", err)
	}
	called := false
	never := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer never.Close()
	for _, key := range []string{"LLM_dashboard", "LLM|inference", ""} {
		if _, err := (Muse{BaseURL: never.URL}).Read(context.Background(), key); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q = %v, want ErrAuth", key, err)
		}
	}
	if called {
		t.Error("a key that is not a device token was sent")
	}
}

func TestMuseUnknownShape(t *testing.T) {
	for _, body := range []string{
		`[]`,
		`{"is_subs_active":"yes"}`,
		`{"is_subs_active":true,"subs_usage":"busy"}`,
		`{"is_subs_active":true,"subs_usage":{"window":{"used_percent":1,"window_duration_mins":"300"},"weekly":{"used_percent":2}}}`,
		`{"is_subs_active":true,"subs_usage":{"window":{"used_percent":1,"window_duration_mins":300}}}`,
	} {
		if _, err := (Muse{BaseURL: museServer(t, body).URL}).Read(context.Background(), "dca:tok"); err == nil {
			t.Errorf("%s was read", body)
		}
	}
}
