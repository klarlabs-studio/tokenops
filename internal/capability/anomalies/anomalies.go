// Package anomalies runs spend anomaly detection (contexts/spend/anomaly)
// over the event store, keeps the answer for the findings to read, and
// offers each unusual day to the operator as an ambient tip they answer
// like any other: acknowledged, expected or snoozed (the tip lifecycle).
//
// It reads the session turns and the daily charges of the last four
// weeks, which takes seconds, so the daemon runs it in the background and
// every surface reads the stored snapshot. The snapshot holds derived
// figures only: dollars, tokens, shares, and the names of models,
// billers, repositories, sessions and tools.
package anomalies

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/anomaly"
)

// KindPrefix opens the follow-through kind of every anomaly tip:
// anomaly_<cause>. Its family is "anomaly".
const KindPrefix = "anomaly_"

// Family is the follow-through family of anomaly tips.
const Family = "anomaly"

// lookback is how much history a run reads: the forecast's window and
// the day it checks.
const lookback = (anomaly.DefaultHistoryDays + 2) * 24 * time.Hour

// expectedFor is how long an "expected" answer keeps its pattern quiet:
// as long as the ledger keeps the answer.
const expectedFor = 90 * 24 * time.Hour

// Anomaly is an unusual day, with what the waste detector found in the
// session a runaway loop names.
type Anomaly struct {
	anomaly.Anomaly
	// Workflow is the runaway session's workflow, for `tokenops replay`.
	Workflow string `json:"workflow,omitempty"`
	// Waste is what the waste detector reported for it.
	Waste []string `json:"waste,omitempty"`
}

// Snapshot is a run's answer.
type Snapshot struct {
	ComputedAt time.Time `json:"computed_at"`
	// HistoryDays is how many days of usage the store holds, up to the
	// window read. Nothing is flagged under MinHistoryDays.
	HistoryDays    int       `json:"history_days"`
	MinHistoryDays int       `json:"min_history_days"`
	Anomalies      []Anomaly `json:"anomalies"`
}

// Deps are the readers a run draws on.
type Deps struct {
	Turns   func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error)
	Charges func(ctx context.Context, since, until time.Time) ([]analytics.Charge, error)
	// RepoOf names the repository each session ran in; nil leaves
	// repositories out.
	RepoOf func(ctx context.Context, since time.Time) map[string]string
	// Waste runs the waste detector over a workflow and returns its
	// findings' summaries; nil leaves them out.
	Waste func(ctx context.Context, workflowID string) []string
	// PlanProviders are the providers a plan covers.
	PlanProviders map[string]bool
	// Ledger is the follow-through ledger offers are recorded in; nil
	// records nothing.
	Ledger coach.Ledger
}

// Run reads the last four weeks, detects, and stores the snapshot in dir.
func Run(ctx context.Context, d Deps, dir string, now time.Time) (Snapshot, error) {
	since := midnight(now.Add(-lookback))
	turns, err := d.Turns(ctx, since)
	if err != nil {
		return Snapshot{}, err
	}
	charges, err := d.Charges(ctx, since, now)
	if err != nil {
		return Snapshot{}, err
	}
	var repoOf map[string]string
	if d.RepoOf != nil {
		repoOf = d.RepoOf(ctx, since)
	}
	items := Items(turns, charges, repoOf)
	s := Snapshot{ComputedAt: now.UTC(), MinHistoryDays: anomaly.DefaultMinHistoryDays, Anomalies: []Anomaly{}}
	if len(items) > 0 {
		first := items[0].Day
		for _, it := range items {
			if it.Day.Before(first) {
				first = it.Day
			}
		}
		s.HistoryDays = int(midnight(now).Sub(first).Hours() / 24)
	}
	for _, a := range anomaly.Detect(items, now, anomaly.Config{PlanProviders: d.PlanProviders}) {
		x := Anomaly{Anomaly: a}
		if a.Cause.Kind == anomaly.CauseRunawayLoop {
			x.Workflow = workflowOf(turns, a.Cause.Value)
			if x.Workflow != "" && d.Waste != nil {
				x.Waste = d.Waste(ctx, x.Workflow)
			}
		}
		s.Anomalies = append(s.Anomalies, x)
	}
	if err := WriteSnapshot(dir, s); err != nil {
		return s, err
	}
	Record(d.Ledger, s, now)
	return s, nil
}

// workflowOf is the workflow most of a session's turns ran under.
func workflowOf(turns []analytics.SessionTurn, session string) string {
	n := map[string]int{}
	for _, t := range turns {
		if t.SessionID == session && t.WorkflowID != "" {
			n[t.WorkflowID]++
		}
	}
	best := ""
	for wf, c := range n {
		if c > n[best] || (c == n[best] && wf < best) {
			best = wf
		}
	}
	return best
}

func midnight(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// itemKey groups turns into items.
type itemKey struct {
	day                                            time.Time
	provider, endpoint, model, repo, session, tool string
	covered                                        bool
}

// chargeKey is a daily charge's cell.
type chargeKey struct {
	day             time.Time
	provider, model string
	covered         bool
}

// Items turns the session turns into items by day and every dimension,
// and adds what each daily charge holds beyond its turns as usage
// outside a session, so the items sum to the day's spend and usage.
func Items(turns []analytics.SessionTurn, charges []analytics.Charge, repoOf map[string]string) []anomaly.Item {
	sums := map[itemKey]*anomaly.Item{}
	inTurns := map[chargeKey]anomaly.Item{}
	for _, t := range turns {
		day := midnight(t.At)
		k := itemKey{day, t.Provider, t.Endpoint, t.Model, repoOf[t.SessionID], t.SessionID, t.Source, t.PlanCovered}
		it, ok := sums[k]
		if !ok {
			it = &anomaly.Item{Day: day, Provider: t.Provider, Endpoint: t.Endpoint, Model: t.Model,
				Repo: k.repo, Session: t.SessionID, Tool: t.Source, PlanCovered: t.PlanCovered}
			sums[k] = it
		}
		it.Tokens += t.Tokens
		it.CostUSD += t.BilledUSD()
		it.ValueUSD += t.APIEquivalentUSD
		ck := chargeKey{day, t.Provider, t.Model, t.PlanCovered}
		c := inTurns[ck]
		c.Tokens += t.Tokens
		c.CostUSD += t.BilledUSD()
		c.ValueUSD += t.APIEquivalentUSD
		inTurns[ck] = c
	}
	out := make([]anomaly.Item, 0, len(sums)+len(charges))
	for _, it := range sums {
		out = append(out, *it)
	}
	for _, c := range charges {
		day := midnight(c.Day)
		seen := inTurns[chargeKey{day, c.Provider, c.Model, c.PlanCovered}]
		rest := anomaly.Item{Day: day, Provider: c.Provider, Model: c.Model, PlanCovered: c.PlanCovered,
			Tokens:  max(0, c.Tokens()-seen.Tokens),
			CostUSD: max(0, c.CostUSD-seen.CostUSD), ValueUSD: max(0, c.ListUSD-seen.ValueUSD)}
		if rest.CostUSD >= 0.005 || rest.ValueUSD >= 0.005 {
			out = append(out, rest)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out
}

// Record offers each anomaly not offered before as an ambient tip in the
// follow-through ledger, under the anomaly's ID, with its pattern as the
// evidence an "expected" answer quiets.
func Record(l coach.Ledger, s Snapshot, now time.Time) {
	if l == nil || len(s.Anomalies) == 0 {
		return
	}
	entries, err := l.Load()
	if err != nil {
		return
	}
	offered := map[string]bool{}
	for _, e := range entries {
		if e.Type == ft.EntryOffer {
			offered[e.ID] = true
		}
	}
	var add []ft.Entry
	for _, a := range s.Anomalies {
		if offered[a.ID] {
			continue
		}
		offered[a.ID] = true
		add = append(add, ft.Entry{Type: ft.EntryOffer, ID: a.ID, At: now.UTC(), Power: config.PowerInform,
			Channel: ft.ChannelTip, Kind: KindPrefix + a.Cause.Kind, Evidence: a.Cause.Pattern, Via: ft.ViaAmbient})
	}
	_ = l.Append(add...)
}

// Open is what of the snapshot still stands at now: anomalies of
// yesterday or today, not acknowledged, not called expected (by
// pattern), and not under a running snooze of any anomaly tip. A ledger
// that cannot be read holds nothing back.
func Open(s Snapshot, l coach.Ledger, now time.Time) []Anomaly {
	var records []ft.Record
	if l != nil {
		if entries, err := l.Load(); err == nil {
			records = ft.Fold(entries, now)
		}
	}
	answered, expected := map[string]bool{}, map[string]bool{}
	for _, r := range records {
		if r.Offer.Channel != ft.ChannelTip || ft.Family(r.Offer.Kind) != Family {
			continue
		}
		switch r.Answer {
		case ft.OutcomeSnoozed:
			if now.Before(r.SnoozedUntil) {
				return nil
			}
		case ft.OutcomeAcknowledged:
			answered[r.Offer.ID] = true
		case ft.OutcomeExpected:
			answered[r.Offer.ID] = true
			if now.Sub(r.Offer.At) <= expectedFor && r.Offer.Evidence != "" {
				expected[r.Offer.Evidence] = true
			}
		}
	}
	yesterday := midnight(now).AddDate(0, 0, -1)
	var out []Anomaly
	for _, a := range s.Anomalies {
		if a.Day.Before(yesterday) || answered[a.ID] || expected[a.Cause.Pattern] {
			continue
		}
		out = append(out, a)
	}
	return out
}

// snapshotFile names the stored run under the cache directory.
const snapshotFile = "spend-anomalies.json"

// ReadSnapshot reads the last run; nil when none has run.
func ReadSnapshot(dir string) (*Snapshot, error) {
	b, err := os.ReadFile(filepath.Join(dir, snapshotFile)) //nolint:gosec // TokenOps' own cache directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// WriteSnapshot stores s in one rename, so a reader never sees half.
func WriteSnapshot(dir string, s Snapshot) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, snapshotFile+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, snapshotFile))
}
