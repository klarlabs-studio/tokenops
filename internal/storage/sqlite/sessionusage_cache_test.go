package sqlite

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// sessionTurn is a prompt event of session, with every field
// SessionUsage reads set from rng.
func sessionTurn(id string, at time.Time, session string, rng *rand.Rand) *eventschema.Envelope {
	e := promptAt(id, at, eventschema.ProviderAnthropic, rng.Int63n(5000)+1)
	p := e.Payload.(*eventschema.PromptEvent)
	p.SessionID = session
	p.CachedInputTokens = rng.Int63n(p.InputTokens + 1)
	p.CacheWriteInputTokens = rng.Int63n(100)
	p.CacheWrite1hInputTokens = rng.Int63n(p.CacheWriteInputTokens + 1)
	p.Latency = time.Duration(rng.Int63n(int64(time.Minute)))
	p.WorkflowID = fmt.Sprintf("wf-%d", rng.Intn(4))
	if rng.Intn(4) != 0 {
		p.AgentID = fmt.Sprintf("ag-%d", rng.Intn(3))
	}
	switch rng.Intn(4) {
	case 0:
		p.CostSource = eventschema.CostSourcePlanIncluded
	case 1:
		p.CostUSD = float64(rng.Intn(1000)) / 100
	}
	if rng.Intn(3) == 0 {
		p.RequestModel = "gpt-5"
		p.Provider = eventschema.ProviderOpenAI
	}
	e.Association.Execution = fmt.Sprintf("ex-%d", rng.Intn(3))
	if rng.Intn(2) == 0 {
		e.Attributes["endpoint"] = "api.example"
	}
	return e
}

// seedSessions stores n events over the last span: session turns, some at
// the same instant, with turns outside a session, readings without usage
// and turns of an excluded source among them.
func seedSessions(t testing.TB, s *Store, prefix string, n int, span time.Duration) {
	t.Helper()
	rng := rand.New(rand.NewSource(int64(n)))
	envs := make([]*eventschema.Envelope, 0, n)
	for i := range n {
		at := cacheNow.Add(-time.Duration(rng.Int63n(int64(span)))).Truncate(time.Minute)
		e := sessionTurn(fmt.Sprintf("%s%05d", prefix, i), at, fmt.Sprintf("s%d", rng.Intn(50)), rng)
		switch rng.Intn(10) {
		case 0: // outside a session
			e.Payload.(*eventschema.PromptEvent).SessionID = ""
		case 1: // a plan-window reading: no model, no tokens
			p := e.Payload.(*eventschema.PromptEvent)
			p.RequestModel, p.InputTokens, p.OutputTokens = "", 0, 0
			p.CachedInputTokens, p.CacheWriteInputTokens, p.CacheWrite1hInputTokens = 0, 0, 0
		case 2:
			e.Source = "mcp-session"
		}
		envs = append(envs, e)
	}
	if err := s.AppendBatch(context.Background(), envs); err != nil {
		t.Fatal(err)
	}
}

func newTestSessionCache(t *testing.T, s *Store) *SessionUsageCache {
	t.Helper()
	c := NewSessionUsageCache(s, 0, time.Hour)
	c.now = func() time.Time { return cacheNow }
	return c
}

// namedFilter is a read a test checks.
type namedFilter struct {
	name string
	f    analytics.Filter
}

// sessionFilters are the reads the cache answers, the widest first, and
// those it hands to the store, that each step checks.
func sessionFilters() []namedFilter {
	month := cacheNow.Add(-30 * 24 * time.Hour)
	return []namedFilter{
		{"month", analytics.Filter{Since: month}},
		{"two weeks", analytics.Filter{Since: cacheNow.Add(-14 * 24 * time.Hour)}},
		{"last hour", analytics.Filter{Since: cacheNow.Add(-time.Hour)}},
		{"a day, closed", analytics.Filter{Since: cacheNow.Add(-3 * 24 * time.Hour), Until: cacheNow.Add(-2 * 24 * time.Hour)}},
		{"every source", analytics.Filter{Since: month, ExcludeSources: []string{}}},
		{"a source readmitted", analytics.Filter{Since: month, IncludeSources: []string{"mcp-session"}}},
		{"another source left out", analytics.Filter{Since: month, ExcludeSources: []string{"claude-code-jsonl"}}},
		{"prompt type named", analytics.Filter{Since: month, EventType: eventschema.EventTypePrompt}},
		{"by provider", analytics.Filter{Since: month, Provider: "openai"}},
		{"by provider, a day", analytics.Filter{Since: cacheNow.Add(-24 * time.Hour), Provider: string(eventschema.ProviderAnthropic)}},
		{"by provider, a week, closed", analytics.Filter{Since: cacheNow.Add(-7 * 24 * time.Hour), Until: cacheNow, Provider: string(eventschema.ProviderOpenAI)}},
		{"by workflow", analytics.Filter{Since: month, WorkflowID: "wf-1"}},
		{"by workflow, a week, source left out", analytics.Filter{Since: cacheNow.Add(-7 * 24 * time.Hour), WorkflowID: "wf-2", ExcludeSources: []string{"claude-code-jsonl"}}},
		{"by agent", analytics.Filter{Since: month, AgentID: "ag-1"}},
		{"by agent, a day", analytics.Filter{Since: cacheNow.Add(-24 * time.Hour), AgentID: "ag-0"}},
		{"by an agent nobody ran", analytics.Filter{Since: month, AgentID: "nobody"}},
		{"by agent and provider", analytics.Filter{Since: month, AgentID: "ag-2", Provider: string(eventschema.ProviderAnthropic)}},
		{"by workflow, agent and provider", analytics.Filter{Since: cacheNow.Add(-7 * 24 * time.Hour), WorkflowID: "wf-3", AgentID: "ag-1", Provider: string(eventschema.ProviderOpenAI)}},
		// Each budget's window, as Measure reads it, scoped as budgets are.
		{"budget, all spend, a day", budgetWindow(budget.WindowDaily, analytics.Filter{})},
		{"budget, all spend, a week", budgetWindow(budget.WindowWeekly, analytics.Filter{})},
		{"budget, all spend, a month", budgetWindow(budget.WindowMonthly, analytics.Filter{})},
		{"budget by provider, a day", budgetWindow(budget.WindowDaily, analytics.Filter{Provider: string(eventschema.ProviderOpenAI)})},
		{"budget by provider, a week", budgetWindow(budget.WindowWeekly, analytics.Filter{Provider: string(eventschema.ProviderAnthropic)})},
		{"budget by provider, a month", budgetWindow(budget.WindowMonthly, analytics.Filter{Provider: string(eventschema.ProviderOpenAI)})},
		{"budget by workflow, a day", budgetWindow(budget.WindowDaily, analytics.Filter{WorkflowID: "wf-0"})},
		{"budget by workflow, a week", budgetWindow(budget.WindowWeekly, analytics.Filter{WorkflowID: "wf-2"})},
		{"budget by workflow, a month", budgetWindow(budget.WindowMonthly, analytics.Filter{WorkflowID: "wf-1"})},
		{"budget by agent, a day", budgetWindow(budget.WindowDaily, analytics.Filter{AgentID: "ag-2"})},
		{"budget by agent, a week", budgetWindow(budget.WindowWeekly, analytics.Filter{AgentID: "ag-0"})},
		{"budget by agent, a month", budgetWindow(budget.WindowMonthly, analytics.Filter{AgentID: "ag-1"})},
		{"budget by all three, a month", budgetWindow(budget.WindowMonthly, analytics.Filter{Provider: string(eventschema.ProviderAnthropic), WorkflowID: "wf-1", AgentID: "ag-1"})},
		// Handed to the store.
		{"by model", analytics.Filter{Since: month, Model: "gpt-5"}},
		{"all time", analytics.Filter{}},
		{"beyond the span", analytics.Filter{Since: cacheNow.Add(-60 * 24 * time.Hour)}},
		{"empty source left", analytics.Filter{Since: month, ExcludeSources: []string{""}}},
	}
}

// budgetWindow is f over the window a budget measures now: from the
// window's start up to now, as budgets.Measure reads it.
func budgetWindow(w budget.Window, f analytics.Filter) analytics.Filter {
	f.Since = budget.WindowStart(w, cacheNow)
	f.Until = cacheNow
	return f
}

// sameSessionUsage fails unless the cache answers each of sessionFilters
// exactly as the store does, order included, for SessionUsage and
// WindowUsage alike.
func sameSessionUsage(t *testing.T, s *Store, c *SessionUsageCache, step string) {
	t.Helper()
	sameWindowUsage(t, s, c, step)
	ctx := context.Background()
	for _, nf := range sessionFilters() {
		name, f := nf.name, nf.f
		want, err := s.SessionUsage(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.SessionUsage(ctx, f)
		if err != nil {
			t.Fatalf("%s, %s: cache: %v", step, name, err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s, %s: cache has %d turns, store %d", step, name, len(got), len(want))
		}
		for i := range want {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Fatalf("%s, %s: turn %d differs:\ncache %+v\nstore %+v", step, name, i, got[i], want[i])
			}
		}
	}
}

// sameWindowUsage fails unless the cache answers each of sessionFilters'
// windows exactly as the store does: the cost to the last bit.
func sameWindowUsage(t *testing.T, s *Store, c *SessionUsageCache, step string) {
	t.Helper()
	ctx := context.Background()
	for _, nf := range sessionFilters() {
		want, err := s.WindowUsage(ctx, nf.f)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.WindowUsage(ctx, nf.f)
		if err != nil {
			t.Fatalf("%s, %s: cache window: %v", step, nf.name, err)
		}
		if !reflect.DeepEqual(got, want) || math.Float64bits(got.Totals.CostUSD) != math.Float64bits(want.Totals.CostUSD) {
			t.Fatalf("%s, %s: window differs:\ncache %+v\nstore %+v", step, nf.name, got, want)
		}
	}
}

func sessionFullReads(c *SessionUsageCache) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fullReads
}

// Each kind of change the store sees is applied to the cached window
// without reading it again, and the cache answers what the store does;
// a VACUUM, which may renumber rowids, at most reads it once more.
func TestSessionUsageCacheMatchesTheStore(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedSessions(t, s, "e", 1500, 40*24*time.Hour)
	c := newTestSessionCache(t, s)
	sameSessionUsage(t, s, c, "first read")
	rng := rand.New(rand.NewSource(7))
	twoWeeks := cacheNow.Add(-14 * 24 * time.Hour)
	steps := []struct {
		name string
		do   func()
		// reread is whether the step may make the cache read its window
		// again.
		reread bool
	}{
		{name: "nothing changed", do: func() {}},
		{name: "insert", do: func() {
			if err := s.Append(ctx, sessionTurn("new-1", cacheNow.Add(-time.Minute), "s1", rng)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "inserts at the same instant as stored turns", do: func() {
			at := cacheNow.Add(-2 * time.Hour).Truncate(time.Minute)
			if err := s.AppendBatch(ctx, []*eventschema.Envelope{
				sessionTurn("tie-1", at, "s2", rng), sessionTurn("tie-2", at, "s3", rng), sessionTurn("tie-0", at, "s2", rng),
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "backfill", do: func() {
			if err := s.AppendBatch(ctx, []*eventschema.Envelope{
				sessionTurn("old-1", cacheNow.Add(-20*24*time.Hour), "s4", rng),
				sessionTurn("old-2", cacheNow.Add(-38*24*time.Hour), "s4", rng), // below the window
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "direct update", do: func() {
			execAll(t, s, `UPDATE events SET payload = json_set(payload, '$.cached_input_tokens', 1, '$.latency_ns', 5), input_tokens = 3
				WHERE id IN ('e00003', 'e00100', 'new-1')`)
		}},
		{name: "re-attribution", do: func() {
			if _, err := s.Reattribute(ctx, "claude-code-jsonl", "claude-opus-5", "anthropic", "openrouter", "openrouter.ai", twoWeeks, cacheNow, true); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "restamp", do: func() {
			if _, err := s.RestampPlanIncluded(ctx, "openrouter", cacheNow.Add(-30*24*time.Hour), cacheNow, nil); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a turn leaves its session and another joins one", do: func() {
			execAll(t, s, `UPDATE events SET session_id = NULL WHERE id = 'e00010'`,
				`UPDATE events SET session_id = 'joined' WHERE id IN (SELECT id FROM events WHERE COALESCE(session_id, '') = '' LIMIT 2)`)
		}},
		{name: "a reading gains usage", do: func() {
			execAll(t, s, `UPDATE events SET model = 'claude-opus-5', session_id = 's9'
				WHERE id IN (SELECT id FROM events WHERE COALESCE(model, '') = '' LIMIT 1)`)
		}},
		{name: "a turn moved out of the window and one into it", do: func() {
			execAll(t, s,
				fmt.Sprintf(`UPDATE events SET timestamp_ns = %d WHERE id = 'e00007'`, cacheNow.Add(-36*24*time.Hour).UnixNano()),
				fmt.Sprintf(`UPDATE events SET timestamp_ns = %d WHERE id = 'old-2'`, cacheNow.Add(-2*time.Hour).UnixNano()))
		}},
		{name: "columns left NULL", do: func() {
			execAll(t, s, `UPDATE events SET provider = NULL WHERE id IN ('e00020', 'e00021', 'e00022')`,
				`UPDATE events SET model = NULL WHERE id IN ('e00023', 'e00024')`,
				`UPDATE events SET source = NULL WHERE id IN ('e00025', 'e00026')`,
				`UPDATE events SET input_tokens = NULL, cost_usd = NULL WHERE id IN ('e00027', 'e00028')`,
				`UPDATE events SET output_tokens = NULL, total_tokens = NULL WHERE id = 'e00029'`)
		}},
		{name: "a NULL provider beside an empty one", do: func() {
			at := cacheNow.Add(-6 * time.Hour)
			a, b := sessionTurn("null-provider", at, "s1", rng), sessionTurn("empty-provider", at.Add(time.Second), "s1", rng)
			for _, e := range []*eventschema.Envelope{a, b} {
				p := e.Payload.(*eventschema.PromptEvent)
				p.CostSource, p.CostUSD, p.RequestModel = eventschema.CostSourceMetered, 0, "claude-opus-5"
			}
			if err := s.AppendBatch(ctx, []*eventschema.Envelope{a, b}); err != nil {
				t.Fatal(err)
			}
			execAll(t, s, `UPDATE events SET provider = NULL WHERE id = 'null-provider'`, `UPDATE events SET provider = '' WHERE id = 'empty-provider'`)
		}},
		{name: "deletes", do: func() {
			execAll(t, s, `DELETE FROM events WHERE id IN ('e00005', 'e00200', 'old-1')`)
		}},
		{name: "a row given a new rowid", do: func() {
			execAll(t, s, `UPDATE events SET rowid = rowid + 100000 WHERE id = 'e00011'`,
				`UPDATE events SET rowid = 3000 WHERE id = 'e00012'`)
		}},
		{name: "the newest row deleted and its rowid reused", do: func() {
			execAll(t, s, `DELETE FROM events WHERE rowid = (SELECT MAX(rowid) FROM events)`)
			if err := s.Append(ctx, sessionTurn("reuse-1", cacheNow.Add(-3*time.Hour), "s5", rng)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "the newest rows deleted and their rowids reused", do: func() {
			execAll(t, s, `DELETE FROM events WHERE rowid IN (SELECT rowid FROM events ORDER BY rowid DESC LIMIT 3)`)
			if err := s.AppendBatch(ctx, []*eventschema.Envelope{
				sessionTurn("reuse-2", cacheNow.Add(-4*time.Hour), "s6", rng),
				sessionTurn("reuse-3", cacheNow.Add(-25*24*time.Hour), "s6", rng),
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "vacuum after deletes", reread: true, do: func() {
			execAll(t, s, `DELETE FROM events WHERE rowid % 9 = 0`, `VACUUM`)
		}},
		{name: "insert after all of it", reread: true, do: func() {
			if err := s.Append(ctx, sessionTurn("new-2", cacheNow, "s1", rng)); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, st := range steps {
		st.do()
		sameSessionUsage(t, s, c, st.name)
		if n := sessionFullReads(c); n != 1 && (!st.reread || n != 2) {
			t.Fatalf("%s: the window was read in full %d times", st.name, n)
		}
	}
}

// The store sums a window's costs in events_usage_idx's order, which for
// events of one instant is not the order the cache holds them in; the
// cache sums them in the store's order, to the last bit.
func TestWindowUsageCacheSumsOneInstantInTheIndexOrder(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	// Summed in rowid order these costs give another last bit than in
	// model order, the index's order here.
	costs := []float64{5.1e-14, 0.007243800000000001, 3.4722e+29, 7.891900000000001e+30, 0.00031263, 0.00093931, 0.056314}
	inserted := []int{0, 1, 6, 2, 5, 3, 4}
	var byRowid sqlSum
	for _, i := range inserted {
		byRowid.add(costs[i])
	}
	rng := rand.New(rand.NewSource(3))
	at := cacheNow.Add(-5 * time.Hour)
	envs := make([]*eventschema.Envelope, 0, len(costs))
	for _, i := range inserted {
		e := sessionTurn(fmt.Sprintf("costly-%d", i), at, "s1", rng)
		p := e.Payload.(*eventschema.PromptEvent)
		p.CostSource, p.CostUSD, p.RequestModel = eventschema.CostSourceMetered, costs[i], fmt.Sprintf("m%d", i)
		p.Provider = eventschema.ProviderAnthropic
		envs = append(envs, e)
	}
	if err := s.AppendBatch(ctx, envs); err != nil {
		t.Fatal(err)
	}
	f := analytics.Filter{Since: cacheNow.Add(-24 * time.Hour)}
	want, err := s.WindowUsage(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if math.Float64bits(want.Totals.CostUSD) == math.Float64bits(byRowid.value()) {
		t.Fatal("the costs sum the same in either order: the test checks nothing")
	}
	got, err := newTestSessionCache(t, s).WindowUsage(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if math.Float64bits(got.Totals.CostUSD) != math.Float64bits(want.Totals.CostUSD) || !reflect.DeepEqual(got, want) {
		t.Fatalf("cache %+v, store %+v", got, want)
	}
}

// Rowids renumbered without a trigger firing, as a VACUUM may: the cache
// cannot trust them, reads its window again, and still answers what the
// store does.
func TestSessionUsageCacheAfterRowidsAreRenumbered(t *testing.T) {
	s := newTestStore(t)
	seedSessions(t, s, "e", 400, 20*24*time.Hour)
	c := newTestSessionCache(t, s)
	sameSessionUsage(t, s, c, "first read")
	execAll(t, s, `DELETE FROM events WHERE rowid % 7 = 0`)
	sameSessionUsage(t, s, c, "after deletes")
	execAll(t, s,
		`DROP TRIGGER events_changes_on_update`,
		`DROP TRIGGER events_changes_on_delete`,
		`CREATE TEMP TABLE renumber AS SELECT * FROM events ORDER BY rowid`,
		`DELETE FROM events`,
		`INSERT INTO events SELECT * FROM renumber`,
		`DROP TABLE renumber`)
	sameSessionUsage(t, s, c, "after renumbering")
	if n := sessionFullReads(c); n != 2 {
		t.Fatalf("renumbered rowids gave %d full reads, want a second", n)
	}
}

// A warm read with nothing changed reads nothing in full, and neither does
// one that only reaches less far back.
func TestSessionUsageCacheWarmReadIsIncremental(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedSessions(t, s, "e", 300, 30*24*time.Hour)
	c := newTestSessionCache(t, s)
	since := cacheNow.Add(-29 * 24 * time.Hour)
	for i := range 3 {
		if _, err := c.SessionUsage(ctx, analytics.Filter{Since: since}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.SessionUsage(ctx, analytics.Filter{Since: since.Add(time.Duration(i) * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if n := sessionFullReads(c); n != 1 {
		t.Fatalf("warm reads with nothing changed read the window %d times, want once", n)
	}
	// A read reaching further back than the window reads it again.
	if _, err := c.SessionUsage(ctx, analytics.Filter{Since: since.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if n := sessionFullReads(c); n != 2 {
		t.Fatalf("a read reaching further back read the window %d times in all, want twice", n)
	}
}

// Reads the cache does not answer go to the store and leave it empty.
func TestSessionUsageCacheLeavesOtherReadsToTheStore(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedSessions(t, s, "e", 100, 10*24*time.Hour)
	c := newTestSessionCache(t, s)
	for name, f := range map[string]analytics.Filter{
		"by model":        {Since: cacheNow.Add(-time.Hour), Model: "gpt-5"},
		"another type":    {Since: cacheNow.Add(-time.Hour), EventType: eventschema.EventTypeWorkflow},
		"all time":        {},
		"beyond the span": {Since: cacheNow.Add(-50 * 24 * time.Hour)},
	} {
		if _, err := c.SessionUsage(ctx, f); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := c.WindowUsage(ctx, f); err != nil {
			t.Fatalf("%s: window: %v", name, err)
		}
		c.mu.Lock()
		held := len(c.windows)
		c.mu.Unlock()
		if held != 0 {
			t.Fatalf("%s: the cache held a window for a read it does not answer", name)
		}
	}
}

// A budget scoped to a provider, a workflow or an agent is answered from
// the cache, for the windows a budget measures (daily, weekly, monthly).
func TestSessionUsageCacheAnswersScopedReads(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedSessions(t, s, "e", 300, 40*24*time.Hour)
	for _, nf := range sessionFilters() {
		if nf.f.Provider == "" && nf.f.WorkflowID == "" && nf.f.AgentID == "" {
			continue
		}
		c := newTestSessionCache(t, s)
		if _, err := c.WindowUsage(ctx, nf.f); err != nil {
			t.Fatalf("%s: %v", nf.name, err)
		}
		c.mu.Lock()
		held := len(c.windows)
		c.mu.Unlock()
		if held == 0 {
			t.Fatalf("%s: the cache handed a scoped window to the store", nf.name)
		}
	}
}

// The cache empties after its idle time.
func TestSessionUsageCacheEmptiesWhenIdle(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedSessions(t, s, "e", 50, 5*24*time.Hour)
	c := NewSessionUsageCache(s, 0, 20*time.Millisecond)
	c.now = func() time.Time { return cacheNow }
	if _, err := c.SessionUsage(ctx, analytics.Filter{Since: cacheNow.Add(-24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		held := len(c.windows)
		c.mu.Unlock()
		if held == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the cache kept its rows past its idle time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A cached row shares its session's strings with the other turns of the
// session.
func TestSessionUsageCacheSharesSessionStrings(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	rng := rand.New(rand.NewSource(1))
	at := cacheNow.Add(-time.Hour)
	a, b := sessionTurn("a", at, "s1", rng), sessionTurn("b", at.Add(time.Second), "s1", rng)
	b.Payload = a.Payload
	b.Attributes = a.Attributes
	b.Association = a.Association
	if err := s.AppendBatch(ctx, []*eventschema.Envelope{a, b}); err != nil {
		t.Fatal(err)
	}
	c := newTestSessionCache(t, s)
	if _, err := c.SessionUsage(ctx, analytics.Filter{Since: cacheNow.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rows := c.windows[struct{}{}].rows
	if len(rows) != 2 || rows[0].key != rows[1].key {
		t.Fatalf("two turns of one session hold %d keys", len(rows))
	}
}

// The store's own read of session usage, which answers the reads past a
// cache's span (the team upload's six weeks), shares the strings that
// repeat between rows: each its own allocation, a busy machine's six weeks
// of turns held about 100 MB of copies of a few session IDs, providers
// and models until the reader was done.
func TestStoreSessionUsageSharesRepeatedStrings(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	rng := rand.New(rand.NewSource(1))
	at := cacheNow.Add(-time.Hour)
	a, b := sessionTurn("a", at, "s1", rng), sessionTurn("b", at.Add(time.Second), "s1", rng)
	b.Payload = a.Payload
	b.Attributes = a.Attributes
	b.Association = a.Association
	if err := s.AppendBatch(ctx, []*eventschema.Envelope{a, b}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.SessionUsage(ctx, analytics.Filter{Since: cacheNow.Add(-2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	for name, pair := range map[string][2]string{
		"session":  {rows[0].SessionID, rows[1].SessionID},
		"provider": {rows[0].Provider, rows[1].Provider},
		"model":    {rows[0].Model, rows[1].Model},
		"source":   {rows[0].Source, rows[1].Source},
	} {
		if pair[0] == "" || pair[0] != pair[1] {
			t.Fatalf("%s: %q and %q, want the same", name, pair[0], pair[1])
		}
		if unsafe.StringData(pair[0]) != unsafe.StringData(pair[1]) {
			t.Errorf("the two turns hold their own copies of the %s %q", name, pair[0])
		}
	}
}

// The warm read of a month of session usage, against reading it from the
// store as before.
func BenchmarkSessionUsage(b *testing.B) {
	ctx := context.Background()
	s, err := Open(ctx, b.TempDir()+"/events.db", Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	seedSessions(b, s, "e", 30_000, 30*24*time.Hour)
	f := analytics.Filter{Since: cacheNow.Add(-29 * 24 * time.Hour)}
	b.Run("store", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := s.SessionUsage(ctx, f); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("cache-warm", func(b *testing.B) {
		c := NewSessionUsageCache(s, 0, time.Hour)
		c.now = func() time.Time { return cacheNow }
		if _, err := c.SessionUsage(ctx, f); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		for b.Loop() {
			if _, err := c.SessionUsage(ctx, f); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// A budget scoped to one provider, workflow or agent, measured over a
// week as the boundary figures measure it every minute: from the warm
// cache against the store.
func BenchmarkScopedWindowUsage(b *testing.B) {
	ctx := context.Background()
	s, err := Open(ctx, b.TempDir()+"/events.db", Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	seedSessions(b, s, "e", 30_000, 30*24*time.Hour)
	since := cacheNow.Add(-7 * 24 * time.Hour)
	for _, scope := range []struct {
		name string
		f    analytics.Filter
	}{
		{"provider", analytics.Filter{Since: since, Until: cacheNow, Provider: string(eventschema.ProviderAnthropic)}},
		{"workflow", analytics.Filter{Since: since, Until: cacheNow, WorkflowID: "wf-1"}},
		{"agent", analytics.Filter{Since: since, Until: cacheNow, AgentID: "ag-1"}},
	} {
		b.Run(scope.name+"/store", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := s.WindowUsage(ctx, scope.f); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(scope.name+"/cache-warm", func(b *testing.B) {
			c := NewSessionUsageCache(s, 0, time.Hour)
			c.now = func() time.Time { return cacheNow }
			if _, err := c.WindowUsage(ctx, scope.f); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := c.WindowUsage(ctx, scope.f); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// A week's window usage, as a budget measures it every minute, from the
// warm cache against reading it from the store as before.
func BenchmarkWindowUsage(b *testing.B) {
	ctx := context.Background()
	s, err := Open(ctx, b.TempDir()+"/events.db", Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	seedSessions(b, s, "e", 30_000, 30*24*time.Hour)
	f := analytics.Filter{Since: cacheNow.Add(-7 * 24 * time.Hour), Until: cacheNow}
	b.Run("store", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := s.WindowUsage(ctx, f); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("cache-warm", func(b *testing.B) {
		c := NewSessionUsageCache(s, 0, time.Hour)
		c.now = func() time.Time { return cacheNow }
		if _, err := c.WindowUsage(ctx, f); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		for b.Loop() {
			if _, err := c.WindowUsage(ctx, f); err != nil {
				b.Fatal(err)
			}
		}
	})
}
