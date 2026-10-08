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

// codebuffServer answers POST /api/v1/usage with body for the key "cb",
// and checks the request is the one Codebuff's CLI sends.
func codebuffServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cb" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			FingerprintID string `json:"fingerprintId"`
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/usage" || r.Header.Get("Content-Type") != "application/json" ||
			json.NewDecoder(r.Body).Decode(&req) != nil || req.FingerprintID == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCodebuffReadsTheCredits(t *testing.T) {
	srv := codebuffServer(t, fixture(t, "codebuff"))
	got, err := Codebuff{BaseURL: srv.URL}.Read(context.Background(), "cb")
	if err != nil || !got.Subscription || len(got.Windows) != 1 || got.HasBalance {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "credits" || !approx(w.UsedPct, 25) || !w.ResetsAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("credits %+v", w)
	}
}

func TestCodebuffShapes(t *testing.T) {
	cases := []struct {
		name, body string
		windows    int
		used       float64
	}{
		{"used and remaining, no quota", `{"usage":30,"remainingBalance":70,"next_quota_reset":1793491200}`, 1, 30},
		{"no quota to spend against", `{"usage":0,"quota":0,"remainingBalance":0}`, 1, 100},
		{"unknown shape", `{"something":"else"}`, 0, 0},
	}
	for _, c := range cases {
		srv := codebuffServer(t, c.body)
		got, err := Codebuff{BaseURL: srv.URL}.Read(context.Background(), "cb")
		if err != nil || len(got.Windows) != c.windows {
			t.Errorf("%s: got %+v, %v", c.name, got, err)
			continue
		}
		if c.windows > 0 && !approx(got.Windows[0].UsedPct, c.used) {
			t.Errorf("%s: used %v, want %v", c.name, got.Windows[0].UsedPct, c.used)
		}
	}
	srv := codebuffServer(t, `{"usage":30,"remainingBalance":70,"next_quota_reset":1793491200}`)
	got, _ := Codebuff{BaseURL: srv.URL}.Read(context.Background(), "cb")
	if !got.Windows[0].ResetsAt.Equal(time.Unix(1793491200, 0)) {
		t.Errorf("a reset in Unix seconds = %v", got.Windows[0].ResetsAt)
	}
}

func TestCodebuffRefusesTheKey(t *testing.T) {
	srv := codebuffServer(t, fixture(t, "codebuff"))
	if _, err := (Codebuff{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a refused key = %v, want ErrAuth", err)
	}
}
