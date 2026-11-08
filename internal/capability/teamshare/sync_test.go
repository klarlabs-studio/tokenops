package teamshare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

func TestAllowHoldsTheUploadBeforeAnythingIsSent(t *testing.T) {
	sent := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sent = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "team.json")
	if err := teamclient.Save(path, teamclient.State{URL: srv.URL, DeviceToken: "tok"}); err != nil {
		t.Fatal(err)
	}
	held := errors.New("held by a boundary")
	asked := 0
	opts := Options{Days: 1, Allow: func() error { asked++; return held }}
	if _, err := Sync(context.Background(), deps(), opts, path, now); !errors.Is(err, held) {
		t.Fatalf("err = %v; want the boundary's", err)
	}
	if sent || asked != 1 {
		t.Fatalf("sent %v, asked %d", sent, asked)
	}
}

func TestAllowIsNotAskedBeforeJoining(t *testing.T) {
	opts := Options{Days: 1, Allow: func() error {
		t.Fatal("a machine that joined no team was asked about sharing")
		return nil
	}}
	_, err := Sync(context.Background(), deps(), opts, filepath.Join(t.TempDir(), "none.json"), now)
	if !errors.Is(err, teamclient.ErrNotJoined) {
		t.Fatalf("err = %v", err)
	}
}

func TestPausedSaysTheFullWindowIsResent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ingest" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_ = json.NewEncoder(w).Encode(teamwire.Error{Error: "trial ended", Code: teamwire.CodeUploadsPaused})
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "team.json")
	if err := teamclient.Save(path, teamclient.State{URL: srv.URL, DeviceToken: "tok"}); err != nil {
		t.Fatal(err)
	}
	// The daemon's uploads between full ones cover two days; a resumed
	// upload resends the full window.
	_, err := Sync(context.Background(), deps(), Options{Days: 2, FullDays: 14}, path, now)
	var paused *PausedError
	if !errors.As(err, &paused) || paused.Days != 14 {
		t.Fatalf("err = %v; want paused with the full 14 days", err)
	}
}
