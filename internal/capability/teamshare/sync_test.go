package teamshare

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/infra/teamclient"
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
