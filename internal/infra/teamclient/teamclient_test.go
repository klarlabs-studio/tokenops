package teamclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/pkg/teamwire"
)

func TestStateRoundTripIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "team.json")
	if _, err := Load(path); !errors.Is(err, ErrNotJoined) {
		t.Fatalf("missing state: %v", err)
	}
	s := State{URL: "https://team.example.eu", DeviceToken: "tot_dev_x", TeamName: "platform"}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("state file mode %v, want 0600", info.Mode().Perm())
	}
	got, err := Load(path)
	if err != nil || got.TeamName != "platform" {
		t.Errorf("load = %+v, %v", got, err)
	}
	if err := Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path); err != nil {
		t.Errorf("second remove: %v", err)
	}
}

func TestCheckURL(t *testing.T) {
	for in, ok := range map[string]bool{
		"https://team.example.eu":      true,
		"https://team.example.eu/":     true,
		"http://localhost:8080":        true,
		"http://127.0.0.1:8080":        true,
		"http://team.example.eu":       false,
		"ftp://team.example.eu":        false,
		"team.example.eu":              false,
		"https://u:p@team.example.eu":  false,
		"https://team.example.eu/?x=1": false,
	} {
		if _, err := CheckURL(in); (err == nil) != ok {
			t.Errorf("CheckURL(%q) err = %v, want ok=%v", in, err, ok)
		}
	}
}

func TestClientSendsBearerAndReadsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(teamwire.Error{Error: "unauthorized"})
			return
		}
		switch r.URL.Path {
		case "/api/v1/ingest":
			_ = json.NewEncoder(w).Encode(teamwire.IngestResponse{Accepted: 3})
		default:
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(teamwire.Error{Error: "refused", Hint: "derived figures only"})
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	r, err := c.Upload(context.Background(), teamwire.Upload{})
	if err != nil || r.Accepted != 3 {
		t.Fatalf("upload = %+v, %v", r, err)
	}
	_, err = c.Me(context.Background())
	var se *ServerError
	if !errors.As(err, &se) || se.Status != http.StatusUnprocessableEntity || se.Hint == "" {
		t.Errorf("error = %v", err)
	}
	if _, err := New(srv.URL, "").Me(context.Background()); !errors.As(err, &se) || se.Status != http.StatusUnauthorized {
		t.Errorf("anonymous = %v", err)
	}
}
