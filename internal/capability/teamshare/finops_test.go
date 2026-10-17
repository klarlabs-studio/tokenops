package teamshare

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/pkg/eventschema"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// teamwire keeps its own copies of these vocabularies so the wire package
// stays light; they must not drift from the domains that define them.
func TestWireVocabulariesMatchTheDomains(t *testing.T) {
	classes := make([]string, 0, len(waste.Classes()))
	for _, c := range waste.Classes() {
		classes = append(classes, string(c))
	}
	for name, c := range map[string][2][]string{
		"waste classes": {teamwire.WasteClasses, classes},
		"verdicts":      {teamwire.AdviceVerdicts, {plans.VerdictStay, plans.VerdictSwitch, plans.VerdictUpgrade}},
		"option kinds":  {teamwire.AdviceKinds, {plans.OptionPlan, plans.OptionPlanOverflow, plans.OptionAPI}},
		"confidences":   {teamwire.AdviceConfidences, {plans.ConfidenceLow, plans.ConfidenceMedium, plans.ConfidenceHigh}},
		"providerless":  {teamwire.ProviderlessPlans, {plans.PayAsYouGo, plans.Subscription}},
	} {
		if !slices.Equal(c[0], c[1]) {
			t.Errorf("%s: teamwire has %v, the domain %v", name, c[0], c[1])
		}
	}
	for _, name := range plans.Names() {
		p, _ := plans.Lookup(name)
		if !teamwire.ValidPlan(p.Provider, name) {
			t.Errorf("catalog plan %s of %s is not a valid wire plan", name, p.Provider)
		}
	}
}

type eventsReader struct{ envs []*eventschema.Envelope }

func (r eventsReader) ReadEvents(_ context.Context, _ eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	var out []*eventschema.Envelope
	for _, e := range r.envs {
		if !e.Timestamp.Before(since) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (eventsReader) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return nil, nil
}

// planEvents is a fictional machine on a Max plan: a 5-hour window that
// ran out on 7 October, and on 8 October three quarters of the tokens on
// the plan, the rest billed per token.
func planEvents() []*eventschema.Envelope {
	day := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	reset := day.Add(13 * time.Hour)
	reading := func(at time.Time, used string) *eventschema.Envelope {
		return &eventschema.Envelope{Source: "claude-usage-meter", Timestamp: at, Attributes: map[string]string{
			"granularity": "quota_snapshot", "five_hour_kind": "session", "five_hour_used_pct": used,
			"five_hour_reset_at": reset.Format(time.RFC3339),
		}, Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic}}
	}
	turn := func(at time.Time, cs eventschema.CostSource, tokens int64) *eventschema.Envelope {
		return &eventschema.Envelope{Source: "claude-code-jsonl", Timestamp: at,
			Attributes: map[string]string{"granularity": "assistant_turn"},
			Payload:    &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, CostSource: cs, TotalTokens: tokens}}
	}
	return []*eventschema.Envelope{
		reading(day.Add(10*time.Hour), "60.00"), reading(day.Add(11*time.Hour), "100.00"),
		turn(now.Add(-3*time.Hour), eventschema.CostSourcePlanIncluded, 750),
		turn(now.Add(-2*time.Hour), eventschema.CostSourceMetered, 250),
	}
}

func TestPlanDays(t *testing.T) {
	// The plan moved from Max 5x to Max 20x at the start of 8 October;
	// "claude-max" is the old alias of Max 20x. A binding the catalog does
	// not know is not sent.
	history := plans.History{
		{Provider: "anthropic", Plan: "claude-max-5x", From: now.AddDate(0, 0, -30)},
		{Provider: "anthropic", Plan: "claude-max", From: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)},
	}
	bound := map[string]string{"anthropic": "claude-max", "openai": "a plan of my own"}
	got := PlanDays(planEvents(), bound, history, []string{"2026-10-06", "2026-10-07", "2026-10-08"}, now)
	if len(got) != 2 {
		t.Fatalf("plan days = %+v", got)
	}
	d7, d8 := got[0], got[1]
	if d7.Day != "2026-10-07" || d7.Plan != "claude-max-5x" || d7.Lockouts != 1 || d7.BlockedMinutes != 120 ||
		d7.UtilizationPct == nil || *d7.UtilizationPct != 100 || d7.UtilizationWindows != 1 || d7.CoveragePct != nil {
		t.Errorf("7 October = %+v", d7)
	}
	if d8.Day != "2026-10-08" || d8.Plan != "claude-max-20x" || d8.Lockouts != 0 || d8.UtilizationPct != nil ||
		d8.CoveragePct == nil || *d8.CoveragePct != 75 || d8.PlanTokens != 750 || d8.BilledTokens != 250 {
		t.Errorf("8 October = %+v", d8)
	}
	for _, r := range got {
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", r.Day, err)
		}
	}
}

// wasteDays is two fictional days of the waste report: on 8 October two
// turns of sess-1 (in acme/api, edit work) had re-reads and a retry, and
// one turn of a session outside any repository a cache rebuild.
func wasteDays() []avoidable.Day {
	t0 := now.Add(-2 * time.Hour)
	tw := func(session string, at time.Time, c waste.Class, tokens int64, usd float64) avoidable.TurnWaste {
		return avoidable.TurnWaste{Turn: analytics.SessionTurn{SessionID: session, At: at, Model: "claude-opus"},
			Class: c, Tokens: tokens, APIEquivalentUSD: usd}
	}
	d8 := avoidable.Day{Start: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
		Turns: []avoidable.TurnWaste{
			tw("sess-1", t0.Add(time.Minute), waste.ClassReread, 100, 0.05),
			tw("sess-1", t0.Add(11*time.Minute), waste.ClassReread, 50, 0.02),
			tw("sess-1", t0.Add(11*time.Minute), waste.ClassRetryLoop, 30, 0.01),
			tw("sess-9", t0, waste.ClassCacheRebuild, 70, 0.03),
		},
		Savings: savings.Report{Interventions: 3, Held: 1},
	}
	d8.Report.Avoidable.APIEquivalentUSD = 0.11
	d8.Report.Realized = avoidable.Realized{HoldoutUSD: measurement.Derived(0.4, "holdout"), RefusedRereads: 1,
		RefusedTokens: 400, RefusedUSD: 0.2, USD: 0.6}
	d7 := avoidable.Day{Start: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)}
	d7.Report.Realized.HoldoutUSD = measurement.Unknown("no holdout comparison has concluded yet")
	return []avoidable.Day{d7, d8}
}

func TestWasteRows(t *testing.T) {
	kinds := kinds{"sess-1": {{start: now.Add(-2 * time.Hour), kind: taskclass.KindEdit}}}
	covered := map[string]bool{"2026-10-07": true, "2026-10-08": true}
	got := WasteRows(wasteDays(), map[string]string{"sess-1": "acme/api"}, kinds.at, covered)
	want := []teamwire.Waste{
		{Day: "2026-10-08", Repo: "acme/api", Kind: "edit", Class: "redundant_reread", Turns: 2, Tokens: 150, APIEquivalentUSD: 0.07},
		{Day: "2026-10-08", Repo: "acme/api", Kind: "edit", Class: "retry_loop", Turns: 1, Tokens: 30, APIEquivalentUSD: 0.01},
		{Day: "2026-10-08", Repo: "none", Kind: "unknown", Class: "cache_rebuild", Turns: 1, Tokens: 70, APIEquivalentUSD: 0.03},
	}
	if len(got) != len(want) {
		t.Fatalf("waste = %+v", got)
	}
	for i := range want {
		g := got[i]
		g.APIEquivalentUSD, want[i].APIEquivalentUSD = round4(g.APIEquivalentUSD), round4(want[i].APIEquivalentUSD)
		if g != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func round4(v float64) float64 { return float64(int64(v*10000+0.5)) / 10000 }

func TestSavingsRows(t *testing.T) {
	got := SavingsRows(wasteDays(), map[string]bool{"2026-10-07": true, "2026-10-08": true})
	if len(got) != 1 {
		t.Fatalf("savings = %+v", got)
	}
	s := got[0]
	if s.Day != "2026-10-08" || s.Interventions != 3 || s.Held != 1 || s.RefusedRereads != 1 || s.RefusedTokens != 400 ||
		s.RefusedUSD != 0.2 || s.HoldoutUSD == nil || *s.HoldoutUSD != 0.4 || s.RealizedUSD != 0.6 || s.AvoidableUSD != 0.11 {
		t.Errorf("savings = %+v", s)
	}
}

func TestAdviceRows(t *testing.T) {
	five, twenty := 100.0, 200.0
	advs := []plans.Advice{
		{Provider: "anthropic", Plan: "claude-max-20x", Confidence: plans.ConfidenceMedium, Days: 28, WindowsMeasured: 4,
			Options: []plans.Option{
				{Kind: plans.OptionPlan, Plan: "claude-max-5x", Lockouts: 1, BlockedMinutes: 90, MonthlyUSD: &five},
				{Kind: plans.OptionPlan, Plan: "claude-max-20x", Current: true, MonthlyUSD: &twenty},
			},
			Recommendation: &plans.Recommendation{Verdict: plans.VerdictSwitch, Kind: plans.OptionPlan, Plan: "claude-max-5x",
				SavingsUSD: 100, Material: true, Summary: "Max 5x would have saved $100 a month"},
			Caveats: []string{"a caveat in prose"}},
		// Nothing to replay: no recommendation, no row.
		{Provider: "openai", Plan: "gpt-plus", Confidence: plans.ConfidenceLow},
	}
	got := AdviceRows(advs)
	want := teamwire.PlanAdvice{Provider: "anthropic", Plan: "claude-max-20x", Verdict: "switch", Kind: "plan",
		RecommendedPlan: "claude-max-5x", MonthlyDeltaUSD: 100, Material: true, RecommendedLockouts: 1,
		RecommendedBlockedMinutes: 90, Confidence: "medium", Days: 28, WindowsMeasured: 4}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("advice = %+v", got)
	}
	raw, _ := json.Marshal(got)
	for _, prose := range []string{"saved", "caveat"} {
		if strings.Contains(string(raw), prose) {
			t.Errorf("advice carries prose %q: %s", prose, raw)
		}
	}
}

func finopsDeps() Deps {
	d := deps()
	cfg := &config.Config{Plans: map[string]string{"anthropic": "claude-max-20x"}}
	d.FinOps = FinOps{
		Plans: func() headroom.Deps { return headroom.Deps{Config: cfg, Reader: eventsReader{planEvents()}} },
		Waste: func(context.Context, time.Time, time.Time) ([]avoidable.Day, error) { return wasteDays(), nil },
	}
	return d
}

// A schema 2 upload is built from the same machine full of prompts and
// paths as schema 1, and carries none of them either.
func TestSchema2UploadCarriesNoContent(t *testing.T) {
	u, warnings := Build(context.Background(), finopsDeps(), Options{Days: 14, RepoNames: RepoRemote, Schema: teamwire.SchemaFinOps}, now)
	if len(warnings) > 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if u.Schema != teamwire.SchemaFinOps || len(u.PlanDays) == 0 || len(u.Waste) == 0 || len(u.Savings) == 0 {
		t.Fatalf("schema 2 upload = %+v", u)
	}
	if err := u.Validate(); err != nil {
		t.Fatalf("schema 2 upload does not validate: %v", err)
	}
	raw, _ := json.Marshal(u)
	for _, c := range canaries {
		if strings.Contains(string(raw), c) {
			t.Errorf("upload carries %q:\n%s", c, raw)
		}
	}
	// The waste is filed under the same repository and kind as the work.
	if !slices.ContainsFunc(u.Waste, func(w teamwire.Waste) bool { return w.Repo == "acme/api" && w.Kind == "edit" }) {
		t.Errorf("waste not filed with the work: %+v", u.Waste)
	}
}

// fakeServer is a team server that answers Me with maxSchema (absent when
// zero) and keeps each upload's body.
type fakeServer struct {
	maxSchema int
	meCalls   int
	bodies    [][]byte
}

func (f *fakeServer) start(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/me":
			f.meCalls++
			_ = json.NewEncoder(w).Encode(teamwire.Me{OrgName: "Example Org", MaxSchema: f.maxSchema})
		case "/api/v1/ingest":
			raw, _ := io.ReadAll(r.Body)
			f.bodies = append(f.bodies, raw)
			_ = json.NewEncoder(w).Encode(teamwire.IngestResponse{Accepted: 1})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestSyncNegotiatesTheSchema(t *testing.T) {
	cases := map[string]struct {
		maxSchema  int
		wantSchema int
		newKeys    bool
	}{
		"server without max_schema": {0, 1, false},
		"schema 1 server":           {1, 1, false},
		"schema 2 server":           {2, 2, true},
		"newer server":              {3, 2, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeServer{maxSchema: c.maxSchema}
			path := filepath.Join(t.TempDir(), "team.json")
			if err := teamclient.Save(path, teamclient.State{URL: f.start(t), DeviceToken: "tok"}); err != nil {
				t.Fatal(err)
			}
			res, err := Sync(context.Background(), finopsDeps(), Options{Days: 3}, path, now)
			if err != nil {
				t.Fatal(err)
			}
			if res.Upload.Schema != c.wantSchema || len(f.bodies) != 1 {
				t.Fatalf("sent schema %d, %d uploads", res.Upload.Schema, len(f.bodies))
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal(f.bodies[0], &m); err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"plan_days", "waste", "savings"} {
				if _, has := m[k]; has != c.newKeys {
					t.Errorf("%s sent: %v, want %v", k, has, c.newKeys)
				}
			}
			if string(m["schema"]) != map[int]string{1: "1", 2: "2"}[c.wantSchema] {
				t.Errorf("schema on the wire = %s", m["schema"])
			}
		})
	}
}

// The server's answer is cached for a day: Me is asked once for many
// uploads, and again once the answer is old.
func TestServerSchemaIsCached(t *testing.T) {
	f := &fakeServer{maxSchema: 2}
	path := filepath.Join(t.TempDir(), "team.json")
	if err := teamclient.Save(path, teamclient.State{URL: f.start(t), DeviceToken: "tok"}); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{now, now.Add(time.Hour), now.Add(2 * time.Hour)} {
		if _, err := Sync(context.Background(), deps(), Options{Days: 1}, path, at); err != nil {
			t.Fatal(err)
		}
	}
	if f.meCalls != 1 {
		t.Errorf("Me asked %d times in two hours, want once", f.meCalls)
	}
	f.maxSchema = 0 // the server was rolled back
	res, err := Sync(context.Background(), deps(), Options{Days: 1}, path, now.Add(SchemaRecheck+time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if f.meCalls != 2 || res.Upload.Schema != teamwire.SchemaBuckets {
		t.Errorf("after a day: %d Me calls, schema %d", f.meCalls, res.Upload.Schema)
	}
}

type failingMe struct{}

func (failingMe) Me(context.Context) (teamwire.Me, error) { return teamwire.Me{}, io.ErrUnexpectedEOF }

func TestServerSchemaWhenTheServerCannotBeAsked(t *testing.T) {
	var st teamclient.State
	if got := ServerSchema(context.Background(), failingMe{}, &st, now); got != teamwire.SchemaBuckets || st.SchemaCheckedAt != nil {
		t.Errorf("unknown server: schema %d, checked %v", got, st.SchemaCheckedAt)
	}
	st.NoteSchema(2, now.Add(-48*time.Hour))
	if got := ServerSchema(context.Background(), failingMe{}, &st, now); got != teamwire.SchemaFinOps {
		t.Errorf("stale answer with the server unreachable: schema %d, want the last known 2", got)
	}
}
