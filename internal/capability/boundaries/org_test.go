package boundaries

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// fakeTeam is the team plane's boundaries API.
type fakeTeam struct {
	t        *testing.T
	priv     ed25519.PrivateKey
	mu       sync.Mutex
	doc      teamwire.OrgBoundaries
	requests []teamwire.ApprovalRequest
	answer   teamwire.Approval
}

func newFakeTeam(t *testing.T, rules ...teamwire.BoundaryRule) (*fakeTeam, *httptest.Server) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	f := &fakeTeam{t: t, priv: priv, doc: teamwire.OrgBoundaries{OrgID: "o1", Version: 1, Rules: rules}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeTeam) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/boundaries":
		s, _ := teamwire.SignBoundaries(f.doc, f.priv)
		_ = json.NewEncoder(w).Encode(s)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/approvals":
		var req teamwire.ApprovalRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Validate() != nil {
			f.t.Errorf("bad request %+v, %v", req, err)
		}
		f.requests = append(f.requests, req)
		_ = json.NewEncoder(w).Encode(teamwire.Approval{DecisionID: req.DecisionID, Status: teamwire.ApprovalPending})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/approvals/"):
		_ = json.NewEncoder(w).Encode(f.answer)
	default:
		http.NotFound(w, r)
	}
}

func joined(t *testing.T, env Env, url string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "team.json")
	if err := teamclient.Save(path, teamclient.State{URL: url, DeviceToken: "tok"}); err != nil {
		t.Fatal(err)
	}
	return path
}

var bigSubagents = teamwire.BoundaryRule{
	Name: "big-subagents", Action: "subagent_model", Require: "approver:admin", Stage: "enforce",
	When: map[string]string{"model_tier": "> sonnet"},
}

func TestSyncOrgPinsAndEveryReadVerifies(t *testing.T) {
	env := testEnv(t, false)
	f, srv := newFakeTeam(t, bigSubagents)
	path := joined(t, env, srv.URL)
	c := teamclient.New(srv.URL, "tok")
	if v, err := SyncOrg(context.Background(), c, env.Dir, path); err != nil || v != 1 {
		t.Fatalf("sync = %d, %v", v, err)
	}
	set := LoadOrg(env.Dir, path)
	if set.Problem != "" || len(set.Rules) != 1 || set.Rules[0].Name != "org:big-subagents" || !set.Rules[0].TeamRouted() {
		t.Fatalf("org = %+v", set)
	}

	// An older document is not taken for a newer one.
	f.doc.Version = 0
	if v, _ := SyncOrg(context.Background(), c, env.Dir, path); v != 1 {
		t.Fatalf("older version replaced the held one: %d", v)
	}

	// Rules edited on disk are not rules.
	raw, _, _ := env.Dir.GetOrg()
	var signed teamwire.SignedBoundaries
	_ = json.Unmarshal(raw, &signed)
	signed.Document = []byte(strings.Replace(string(signed.Document), "approver:admin", "allow", 1))
	edited, _ := json.Marshal(signed)
	_ = env.Dir.PutOrg(edited)
	if set := LoadOrg(env.Dir, path); len(set.Rules) != 0 || set.Problem == "" {
		t.Fatalf("an edited document was used: %+v", set)
	}

	// A server signing with another key is refused, and the pin kept.
	_, f.priv, _ = ed25519.GenerateKey(rand.Reader)
	f.doc.Version = 5
	if _, err := SyncOrg(context.Background(), c, env.Dir, path); err == nil {
		t.Fatal("a document under another key was accepted")
	}
}

func TestOrgApproverRulesAreRoutedNeverAnsweredHere(t *testing.T) {
	env := testEnv(t, true) // someone is at the keyboard: still not theirs to answer
	f, srv := newFakeTeam(t, bigSubagents)
	path := joined(t, env, srv.URL)
	c := teamclient.New(srv.URL, "tok")
	if _, err := SyncOrg(context.Background(), c, env.Dir, path); err != nil {
		t.Fatal(err)
	}
	rules := LoadOrg(env.Dir, path).Rules
	sub := Call{Harness: HarnessClaudeCode, SessionID: "s1", ToolUseID: "tu-1", Action: domain.ActionSubagentModel, Model: "opus"}

	res, err := Guard(rules, sub, env)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictDeny || !strings.Contains(res.Reason, "needs approval from admin in your organisation") {
		t.Fatalf("verdict %q, reason %q", res.Verdict, res.Reason)
	}
	if _, err := Decide(env, DecideRequest{ID: res.DecisionID, Decision: "approve"}); err == nil ||
		!strings.Contains(err.Error(), "your organisation's rule") {
		t.Fatalf("a local approval of an organisation rule = %v", err)
	}

	// Routed once, however often the agent retries.
	for range 2 {
		if err := RouteApprovals(context.Background(), c, env, 1); err != nil {
			t.Fatal(err)
		}
		_, _ = Guard(rules, sub, env)
	}
	if len(f.requests) != 1 {
		t.Fatalf("requests = %d; want 1", len(f.requests))
	}
	req := f.requests[0]
	if req.Rule != "big-subagents" || req.Action != "subagent_model" || len(req.Crossings) != 1 ||
		req.Crossings[0].Field != "model_tier" || req.Crossings[0].Value != 3 {
		t.Fatalf("request = %+v", req)
	}
	st, _ := RuleStatuses(rules, env, nil)
	if len(st.Pending) != 1 || !st.Pending[0].Routed || !strings.Contains(st.Pending[0].Approve, "admin in your organisation") {
		t.Fatalf("pending = %+v", st.Pending)
	}

	// The admin approves the next step in the team plane.
	f.answer = teamwire.Approval{DecisionID: res.DecisionID, Status: teamwire.ApprovalApproved, Scope: teamwire.ScopeOnce, AnsweredBy: "Ada"}
	if err := RouteApprovals(context.Background(), c, env, 1); err != nil {
		t.Fatal(err)
	}
	if res, _ := Guard(rules, sub, env); res.Verdict != VerdictNone {
		t.Fatalf("after the organisation approved: %q", res.Reason)
	}
	got := stages(t, env, res.DecisionID)
	if got[len(got)-1] != "applied" {
		t.Errorf("stages %v", got)
	}

	// One step only: the next one is asked afresh, as a new request.
	f.answer = teamwire.Approval{}
	if res, _ := Guard(rules, sub, env); res.Verdict != VerdictDeny {
		t.Fatalf("a one-step approval let a second step through")
	}
	if err := RouteApprovals(context.Background(), c, env, 1); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 2 {
		t.Fatalf("the next step was not asked afresh: %d requests", len(f.requests))
	}
}

func TestAllRulesMergeLocalAndOrg(t *testing.T) {
	env := testEnv(t, false)
	_, srv := newFakeTeam(t, bigSubagents)
	path := joined(t, env, srv.URL)
	if _, err := SyncOrg(context.Background(), teamclient.New(srv.URL, "tok"), env.Dir, path); err != nil {
		t.Fatal(err)
	}
	local := rules(t, spendRule("ask", "enforce"))
	all := append(local, LoadOrg(env.Dir, path).Rules...)
	st, _ := RuleStatuses(all, env, nil)
	if len(st.Rules) != 2 || st.Rules[0].Source != "local" || st.Rules[1].Source != "organisation" ||
		!strings.Contains(st.Rules[1].Note, "approval goes to its admin role") {
		t.Fatalf("status = %+v", st.Rules)
	}
	if _, err := Promote(filepath.Join(t.TempDir(), "c.yaml"), "org:big-subagents"); err == nil ||
		!strings.Contains(err.Error(), "set in the team plane") {
		t.Fatalf("promote org rule = %v", err)
	}
}
