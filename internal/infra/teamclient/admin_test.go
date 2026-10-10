package teamclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/teamwire"
)

func TestWaitDeviceLink(t *testing.T) {
	cases := map[string]struct {
		codes   []string // answers before the last
		final   string   // the last answer's code; "" approves
		wantErr string
		slept   time.Duration
	}{
		"approved after pending": {codes: []string{teamwire.CodeAuthorizationPending}, slept: 10 * time.Second},
		"slow down lengthens":    {codes: []string{teamwire.CodeSlowDown}, slept: 5*time.Second + 10*time.Second},
		"declined":               {final: teamwire.CodeAccessDenied, wantErr: "declined"},
		"expired":                {final: teamwire.CodeExpiredToken, wantErr: "expired"},
		"other refusal surfaces": {final: "boom", wantErr: "boom"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			answers := append(append([]string{}, c.codes...), c.final)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				code := answers[0]
				answers = answers[1:]
				if code == "" {
					_ = json.NewEncoder(w).Encode(teamwire.DeviceLinkResult{Admin: &teamwire.AdminCredential{Token: "x"}})
					return
				}
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(teamwire.Error{Error: code, Code: code})
			}))
			defer srv.Close()
			var slept time.Duration
			res, err := New(srv.URL, "").WaitDeviceLink(context.Background(), teamwire.DeviceLink{DeviceCode: "d", Interval: 5},
				func(_ context.Context, d time.Duration) error { slept += d; return nil })
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil || res.Admin == nil {
				t.Fatalf("res %+v err %v", res, err)
			}
			if slept != c.slept {
				t.Errorf("slept %v, want %v", slept, c.slept)
			}
		})
	}
}

func TestAdminStateExpires(t *testing.T) {
	path := AdminStatePath(filepath.Join(t.TempDir(), "team.json"))
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	if _, err := LoadAdmin(path, now); !errors.Is(err, ErrNoAdmin) {
		t.Fatalf("missing: %v", err)
	}
	if err := SaveAdmin(path, AdminState{URL: "https://t.example", Token: "tot_adm_x", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if s, err := LoadAdmin(path, now); err != nil || s.Token != "tot_adm_x" {
		t.Fatalf("load: %+v %v", s, err)
	}
	if _, err := LoadAdmin(path, now.Add(time.Hour)); !errors.Is(err, ErrNoAdmin) {
		t.Errorf("expired credential loaded: %v", err)
	}
}
