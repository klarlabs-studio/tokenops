package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/teamwire"
)

func TestTeamAdminBoundariesAndApprovals(t *testing.T) {
	var (
		mu       sync.Mutex
		put      []teamwire.BoundaryRule
		answered teamwire.ApprovalAnswer
		answerID string
	)
	asked := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	pending := teamwire.Approval{
		ID: "11111111-1111-4111-8111-111111111111", DecisionID: "bnd-0123456789ab", Rule: "big-subagents",
		Action: "subagent_model", Harness: "claude-code", Role: "admin", Member: "Lars", Status: teamwire.ApprovalPending,
		RequestedAt: asked, Crossings: []teamwire.Crossing{{Field: "model_tier", Op: ">", Value: 3, Threshold: 2}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer adm" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/org-boundaries":
			_ = json.NewEncoder(w).Encode(teamwire.OrgBoundaries{Version: 1, UpdatedAt: asked})
		case "PUT /api/v1/org-boundaries":
			var body struct {
				Rules []teamwire.BoundaryRule `json:"rules"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			put = body.Rules
			_ = json.NewEncoder(w).Encode(teamwire.OrgBoundaries{Version: 2, UpdatedAt: asked, Rules: body.Rules})
		case "GET /api/v1/approvals":
			_ = json.NewEncoder(w).Encode([]teamwire.Approval{pending})
		case "POST /api/v1/approvals/" + pending.ID + "/answer":
			answerID = pending.ID
			_ = json.NewDecoder(r.Body).Decode(&answered)
			done := pending
			done.Status, done.Scope, done.AnsweredBy = teamwire.ApprovalApproved, teamwire.ScopeSession, "Ada"
			_ = json.NewEncoder(w).Encode(done)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv(adminTokenEnv, "adm")
	t.Setenv("TOKENOPS_TEAM_STATE", filepath.Join(t.TempDir(), "team.json"))

	if out, err := runTeam(t, "admin", "boundaries", "--server", srv.URL); err != nil || !strings.Contains(out, "sets no boundaries") {
		t.Fatalf("show = %q, %v", out, err)
	}

	bad := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(bad, []byte("- name: x\n  action: continue\n  require: approver:cfo\n"), 0o600)
	if _, err := runTeam(t, "admin", "boundaries", "--server", srv.URL, "--set", bad); err == nil || !strings.Contains(err.Error(), "not a role") {
		t.Fatalf("an invalid file was sent: %v", err)
	}
	if put != nil {
		t.Fatal("an invalid file reached the server")
	}
	good := filepath.Join(t.TempDir(), "org.yaml")
	_ = os.WriteFile(good, []byte("boundaries:\n  - name: big-subagents\n    action: subagent_model\n    when: { model_tier: \"> sonnet\" }\n    require: approver:admin\n    stage: enforce\n"), 0o600)
	out, err := runTeam(t, "admin", "boundaries", "--server", srv.URL, "--set", good)
	if err != nil || !strings.Contains(out, "Saved as version 2") || !strings.Contains(out, "model_tier > sonnet") {
		t.Fatalf("set = %q, %v", out, err)
	}
	if len(put) != 1 || put[0].Require != "approver:admin" || put[0].Stage != "enforce" {
		t.Fatalf("put %+v", put)
	}

	out, err = runTeam(t, "admin", "approvals", "--server", srv.URL)
	if err != nil || !strings.Contains(out, "model opus > sonnet") || !strings.Contains(out, "Lars") {
		t.Fatalf("approvals = %q, %v", out, err)
	}
	out, err = runTeam(t, "admin", "approve", "bnd-0123456789ab", "--session", "--server", srv.URL)
	if err != nil || !strings.Contains(out, "approved Lars's request under big-subagents, for the rest of the session") {
		t.Fatalf("approve = %q, %v", out, err)
	}
	if answerID != pending.ID || answered.Decision != "approve" || !answered.Session {
		t.Fatalf("answered %s %+v", answerID, answered)
	}
}
