package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

func TestTeamBoundariesFetchAndForgetOnLeave(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/boundaries" {
			http.NotFound(w, r)
			return
		}
		s, _ := teamwire.SignBoundaries(teamwire.OrgBoundaries{OrgID: "o", Version: 3, Rules: []teamwire.BoundaryRule{
			{Name: "floor", Action: "continue", Require: "deny", Stage: "enforce", When: map[string]string{"plan_left_pct": "< 5"}},
		}}, priv)
		_ = json.NewEncoder(w).Encode(s)
	}))
	defer srv.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "team.json")
	if err := teamclient.Save(path, teamclient.State{URL: srv.URL, DeviceToken: "tok"}); err != nil {
		t.Fatal(err)
	}
	tb := &teamBoundaries{path: path, dir: boundaryledger.Dir(filepath.Join(dir, "boundaries")), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	tb.once(context.Background(), true)
	if tb.version != 3 || tb.dir.OrgModTime().IsZero() {
		t.Fatalf("version %d; org rules not held", tb.version)
	}
	if st, _ := teamclient.Load(path); len(st.BoundaryKey) == 0 {
		t.Fatal("the signing key was not pinned")
	}
	if err := teamclient.Remove(path); err != nil {
		t.Fatal(err)
	}
	tb.once(context.Background(), false)
	if !tb.dir.OrgModTime().IsZero() {
		t.Fatal("organisation rules outlived leaving the team")
	}
}
