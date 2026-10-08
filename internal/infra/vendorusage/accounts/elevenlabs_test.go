package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// The credits used of the period's limit are a monthly window, read with
// the key in xi-api-key.
func TestElevenLabsCreditWindow(t *testing.T) {
	srv := serveRoutes(t, "xi-api-key", "el-key", map[string]string{"/v1/user/subscription": fixture(t, "elevenlabs")})
	got, err := ElevenLabs{BaseURL: srv.URL}.Read(context.Background(), "el-key")
	if err != nil || !got.Subscription || len(got.Windows) != 1 || got.HasUsed || got.HasBalance {
		t.Fatalf("%+v %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 25) || w.Duration != 30*24*time.Hour || w.ResetsAt.Unix() != 1783900800 {
		t.Errorf("window %+v", w)
	}
}

// An annual refresh is a year; no refresh period leaves the length unknown.
func TestElevenLabsPeriods(t *testing.T) {
	for body, want := range map[string]string{
		`{"character_count":10,"character_limit":40,"character_refresh_period":"annual_period"}`: "year",
		`{"character_count":10,"character_limit":40}`:                                            "period",
	} {
		srv := serveRoutes(t, "xi-api-key", "k", map[string]string{"/v1/user/subscription": body})
		got, err := ElevenLabs{BaseURL: srv.URL}.Read(context.Background(), "k")
		if err != nil || len(got.Windows) != 1 || got.Windows[0].Name != want || !approx(got.Windows[0].UsedPct, 25) {
			t.Errorf("%s: %+v %v", body, got, err)
		}
	}
}

func TestElevenLabsRefusedKey(t *testing.T) {
	srv := serveRoutes(t, "xi-api-key", "el-key", map[string]string{"/v1/user/subscription": fixture(t, "elevenlabs")})
	if _, err := (ElevenLabs{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("err %v", err)
	}
}

// Without the counts the answer is unknown; a zero limit has no window.
func TestElevenLabsUnknownShape(t *testing.T) {
	srv := serveRoutes(t, "xi-api-key", "k", map[string]string{"/v1/user/subscription": `{"tier":"free"}`})
	if got, err := (ElevenLabs{BaseURL: srv.URL}).Read(context.Background(), "k"); err == nil {
		t.Errorf("read %+v", got)
	}
	zero := serveRoutes(t, "xi-api-key", "k", map[string]string{"/v1/user/subscription": `{"character_count":0,"character_limit":0}`})
	if got, err := (ElevenLabs{BaseURL: zero.URL}).Read(context.Background(), "k"); err != nil || !got.Empty() {
		t.Errorf("zero limit: %+v %v", got, err)
	}
}
