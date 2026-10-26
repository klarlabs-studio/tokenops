package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/boundaries"
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
)

const findingsPauseConfig = `boundaries:
  - name: headroom-pause
    action: continue
    when:
      plan_left_pct: "<= 10"
    require: ask
    stage: shadow
`

type findingsLedger struct {
	mu      sync.Mutex
	entries []ft.Entry
}

func (l *findingsLedger) Append(e ...ft.Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e...)
	return nil
}

func (l *findingsLedger) Load() ([]ft.Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]ft.Entry(nil), l.entries...), nil
}

type findingsFixture struct {
	path    string
	ledger  *findingsLedger
	mu      sync.Mutex
	audited []string
}

// newFindingsServer serves findings drawn from a config with the headroom
// pause in shadow and a day whose tips waited: the pause offers a promote,
// the waiting tips their answers.
func newFindingsServer(t *testing.T) (*Server, *findingsFixture) {
	t.Helper()
	dir := t.TempDir()
	f := &findingsFixture{path: filepath.Join(dir, "config.yaml"), ledger: &findingsLedger{}}
	if err := os.WriteFile(f.path, []byte(findingsPauseConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	report := func(context.Context, time.Time) (findings.Report, error) {
		cfg, err := config.ReadMutable(f.path)
		if err != nil {
			return findings.Report{}, err
		}
		return findings.Compute(findings.Inputs{
			Coach:     &coachcap.Report{},
			CoachHook: &coachhook.Stats{Suppressed: map[string]int{coachhook.SuppressedDailyBudget: 2}},
			Pause: &boundaries.PauseSummary{Configured: true, Rule: config.HeadroomPauseName, Stage: cfg.Boundaries[0].Stage,
				Sessions: 2, PausePoint: true, When: "the tightest plan window has 10% or less left", Require: "ask"},
		}), nil
	}
	dismissals := findings.Dismissals{Path: filepath.Join(dir, "findings-dismissed.json")}
	s := New("127.0.0.1:0",
		WithDashAuth(allowAll{}),
		WithFindings(func() FindingsDeps { return FindingsDeps{Report: report, Dismissals: dismissals} }),
		WithActions(func() ActionDeps {
			return ActionDeps{
				ConfigPath: f.path,
				Coach:      func() (coachcap.Ledger, coachcap.ContextLevers) { return f.ledger, nil },
				Audit: func(_ context.Context, target string, _ map[string]any) {
					f.mu.Lock()
					defer f.mu.Unlock()
					f.audited = append(f.audited, target)
				},
			}
		}))
	return s, f
}

func findingKeys(r findings.Report) string {
	out := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		out = append(out, f.Key)
	}
	return strings.Join(out, ",")
}

func TestFindingsServeKeysAndActions(t *testing.T) {
	s, _ := newFindingsServer(t)
	var r findings.Report
	if code := getAPI(t, s, "/api/findings", &r); code != http.StatusOK {
		t.Fatalf("GET /api/findings = %d", code)
	}
	pause, ok := r.Find("boundary:headroom-pause")
	if !ok || len(pause.Actions) != 1 || pause.Actions[0].ID != findings.ActBoundaryPromote || pause.Actions[0].Confirm == "" {
		t.Fatalf("pause finding = %+v in %s", pause, findingKeys(r))
	}
}

func TestActPromotesTheOfferedBoundaryAndAudits(t *testing.T) {
	s, f := newFindingsServer(t)
	rec := post(s, "/api/findings/act", "application/json", `{"key":"boundary:headroom-pause","action":"boundary.promote"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("act = %d %s", rec.Code, rec.Body)
	}
	var body FindingActResult
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Acted.Rule != config.HeadroomPauseName || body.Acted.Stage != "enforce" {
		t.Errorf("acted = %+v", body.Acted)
	}
	if cfg, _ := config.ReadMutable(f.path); cfg.Boundaries[0].Stage != "enforce" {
		t.Errorf("stage on disk = %q", cfg.Boundaries[0].Stage)
	}
	// The report answered is the one after the change: enforced, the pause
	// offers nothing more.
	if p, ok := body.Findings.Find("boundary:headroom-pause"); ok && len(p.Actions) != 0 {
		t.Errorf("after promote the pause still offers %+v", p.Actions)
	}
	if strings.Join(f.audited, ",") != "finding_action" {
		t.Errorf("audited = %v", f.audited)
	}
	// Promoted, the same request is a conflict: the finding no longer
	// offers it.
	if rec := post(s, "/api/findings/act", "application/json", `{"key":"boundary:headroom-pause","action":"boundary.promote"}`); rec.Code != http.StatusConflict {
		t.Errorf("promote again = %d %s", rec.Code, rec.Body)
	}
}

func TestActAnswersTheWaitingTip(t *testing.T) {
	s, f := newFindingsServer(t)
	coachcap.RecordTip(f.ledger, time.Now().Add(-time.Hour), "tip-1", "s1", "budget_75", coachcap.ViaStopHook)
	rec := post(s, "/api/findings/act", "application/json", `{"key":"coach:tips-waited","action":"tip.ack"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("act = %d %s", rec.Code, rec.Body)
	}
	var body FindingActResult
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Acted.Tip != "tip-1" || body.Acted.Answer != coachcap.AnswerAcknowledged {
		t.Fatalf("acted = %+v, %v", body.Acted, err)
	}
}

// Each refusal leaves the config and the ledger as they were.
func TestActRefusesWhatIsNotOffered(t *testing.T) {
	s, f := newFindingsServer(t)
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"key":"boundary:nope","action":"boundary.promote"}`, http.StatusConflict},
		{`{"key":"coach:tips-waited","action":"boundary.promote"}`, http.StatusConflict},
		{`{"key":"boundary:headroom-pause","action":"tip.ack"}`, http.StatusConflict},
		{`{"key":"boundary:headroom-pause","action":"boundary.demote"}`, http.StatusBadRequest},
		{`{"key":"Boundary Headroom","action":"boundary.promote"}`, http.StatusBadRequest},
		{`{"key":"boundary:headroom-pause","action":"boundary.promote","extra":1}`, http.StatusBadRequest},
	} {
		if rec := post(s, "/api/findings/act", "application/json", tc.body); rec.Code != tc.want {
			t.Errorf("%s = %d %s, want %d", tc.body, rec.Code, rec.Body, tc.want)
		}
	}
	if cfg, _ := config.ReadMutable(f.path); cfg.Boundaries[0].Stage != "shadow" {
		t.Error("a refused action promoted the rule")
	}
	if len(f.ledger.entries) != 0 || len(f.audited) != 0 {
		t.Errorf("a refused action wrote: ledger %v audit %v", f.ledger.entries, f.audited)
	}
}

func TestDismissHidesAndRestores(t *testing.T) {
	s, f := newFindingsServer(t)
	rec := post(s, "/api/findings/dismiss", "application/json", `{"key":"boundary:headroom-pause","for":"week"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss = %d %s", rec.Code, rec.Body)
	}
	var body FindingDismissResult
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body.Findings.Find("boundary:headroom-pause"); ok || body.Findings.Hidden != 1 || body.Until == nil {
		t.Fatalf("after dismiss: %s hidden %d until %v", findingKeys(body.Findings), body.Findings.Hidden, body.Until)
	}
	var r findings.Report
	getAPI(t, s, "/api/findings", &r)
	if _, ok := r.Find("boundary:headroom-pause"); ok || r.Hidden != 1 {
		t.Fatalf("GET after dismiss: %s hidden %d", findingKeys(r), r.Hidden)
	}
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"key":"boundary:nope","for":"day"}`, http.StatusConflict},
		{`{"key":"boundary:headroom-pause","for":"month"}`, http.StatusBadRequest},
		{`{"key":"","for":"day"}`, http.StatusBadRequest},
		{`{"key":"no spaces","for":"day"}`, http.StatusBadRequest},
	} {
		if rec := post(s, "/api/findings/dismiss", "application/json", tc.body); rec.Code != tc.want {
			t.Errorf("%s = %d %s, want %d", tc.body, rec.Code, rec.Body, tc.want)
		}
	}
	rec = post(s, "/api/findings/dismiss", "application/json", `{"for":"none"}`)
	var restored FindingDismissResult
	if err := json.Unmarshal(rec.Body.Bytes(), &restored); err != nil || rec.Code != http.StatusOK || restored.Restored != 1 || restored.Findings.Hidden != 0 {
		t.Fatalf("restore = %d %s", rec.Code, rec.Body)
	}
	if _, ok := restored.Findings.Find("boundary:headroom-pause"); !ok {
		t.Error("restored finding is not shown")
	}
	if strings.Join(f.audited, ",") != "finding_dismiss,finding_dismiss" {
		t.Errorf("audited = %v", f.audited)
	}
}

// The writes are mounted only behind the token.
func TestFindingWritesNeedTheAPIToken(t *testing.T) {
	s := New("127.0.0.1:0", WithFindings(func() FindingsDeps { return FindingsDeps{} }), WithActions(func() ActionDeps { return ActionDeps{} }))
	for _, path := range []string{"/api/findings/dismiss", "/api/findings/act"} {
		if s.ServesAPI(http.MethodPost, path) {
			t.Errorf("%s is mounted without API auth", path)
		}
	}
}
