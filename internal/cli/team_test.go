package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// A stub team server: enrol, me and leave.
func teamStub(t *testing.T) (*httptest.Server, *bool) {
	t.Helper()
	left := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/enroll":
			var req teamwire.EnrollRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Invite != "tot_inv_x" || req.DisplayName != "Ada" {
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(teamwire.Error{Error: "invite is unknown, used or expired"})
				return
			}
			_ = json.NewEncoder(w).Encode(teamwire.EnrollResponse{OrgName: "Acme", TeamName: "platform", DeviceToken: "tot_dev_secret"})
		case "GET /api/v1/me":
			_ = json.NewEncoder(w).Encode(teamwire.Me{DisplayName: "Ada", Role: "member", MinGroupSize: 3,
				Viewers: []teamwire.Viewer{{Name: "Lars", Role: "lead", Scope: "team platform", Reason: "1:1s"}}})
		case "DELETE /api/v1/devices/self":
			left = r.Header.Get("Authorization") == "Bearer tot_dev_secret"
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &left
}

func runTeam(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"team"}, args...))
	err := root.Execute()
	return out.String(), err
}

func TestTeamJoinStatusLeave(t *testing.T) {
	srv, left := teamStub(t)
	state := filepath.Join(t.TempDir(), "team.json")
	t.Setenv("TOKENOPS_TEAM_STATE", state)
	t.Setenv("TOKENOPS_STORAGE_PATH", filepath.Join(t.TempDir(), "events.db"))

	out, err := runTeam(t, "status")
	if err != nil || !strings.Contains(out, "Not joined") {
		t.Fatalf("status before joining: %v\n%s", err, out)
	}
	if _, err := runTeam(t, "join", "http://team.example.eu", "tot_inv_x"); err == nil {
		t.Error("joined over plain http to a remote host")
	}
	out, err = runTeam(t, "join", srv.URL, "tot_inv_x", "--name", "Ada", "--device", "laptop")
	if err != nil || !strings.Contains(out, "Joined Acme, team platform") || !strings.Contains(out, "Never prompts") {
		t.Fatalf("join: %v\n%s", err, out)
	}
	if strings.Contains(out, "tot_dev_secret") {
		t.Error("join printed the device token")
	}
	info, err := os.Stat(state)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state file: %v %v", info, err)
	}
	if _, err := runTeam(t, "join", srv.URL, "tot_inv_x", "--name", "Ada"); err == nil {
		t.Error("joined twice")
	}
	out, err = runTeam(t, "status")
	if err != nil || !strings.Contains(out, "Lars (lead)") || strings.Contains(out, "tot_dev_secret") {
		t.Fatalf("status: %v\n%s", err, out)
	}
	out, err = runTeam(t, "leave")
	if err != nil || !*left || !strings.Contains(out, "erased") {
		t.Fatalf("leave: %v left=%v\n%s", err, *left, out)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Error("state file kept after leaving")
	}
}

func TestTeamPreviewSendsNothing(t *testing.T) {
	t.Setenv("TOKENOPS_TEAM_STATE", filepath.Join(t.TempDir(), "team.json"))
	t.Setenv("TOKENOPS_STORAGE_PATH", filepath.Join(t.TempDir(), "events.db"))
	out, err := runTeam(t, "preview", "--json")
	if err != nil {
		t.Fatalf("preview: %v\n%s", err, out)
	}
	var u teamwire.Upload
	if err := json.Unmarshal([]byte(out), &u); err != nil {
		t.Fatalf("preview --json is not an upload: %v\n%s", err, out)
	}
	if err := u.Validate(); err != nil || len(u.Days) != 14 {
		t.Errorf("preview upload: %v, days %d", err, len(u.Days))
	}
}
