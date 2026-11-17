// Package workquote is the use case behind the quote a task gets before it
// starts (tokenops_prepare_work, `tokenops quote`) and the check of that
// quote after it (tokenops_review_work).
//
// The quote is drawn from a stored basis (ADR 0019): the developer's own
// instructions over the last month, each as its client, model, effort,
// kind of work, list-price cost and tokens, and per plan window the list
// value one percent of it held. The daemon keeps the basis and computes
// it again only when the event log has moved. It holds derived figures
// only: an instruction's words are classified in memory while the basis
// is computed and never stored.
package workquote

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/work/quote"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/internal/tempfile"
)

// Version is the basis's layout and computation version. A basis of
// another version is computed again.
const Version = 1

const basisFile = "work-quote-basis.json"

// rateLookback is how far back plan windows are read for their value
// rates: the quote's month, and the week a weekly window that reset at
// its start had opened before it.
const rateLookback = quote.Lookback + 7*24*time.Hour

// Basis is the stored history a quote is drawn from.
type Basis struct {
	Version int `json:"version"`
	// Position is where the event log stood when the basis was computed.
	Position   sqlite.Position `json:"position"`
	ComputedAt time.Time       `json:"computed_at"`
	Samples    []quote.Sample  `json:"samples"`
	// Rates are each provider's plan windows' list value per percent.
	Rates []plans.ValueRate `json:"rates,omitempty"`
}

// Inputs read what a basis is computed from.
type Inputs struct {
	// Units reads the instructions in [since, now], with their words or
	// the facts derived from them, so their kind can be told.
	Units func(ctx context.Context, since, now time.Time) ([]agentdx.Unit, error)
	// Turns reads the priced model turns recorded since.
	Turns func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error)
	// Windows reads each provider's plan window readings in [since, now],
	// one per window at its peak.
	Windows func(ctx context.Context, since, now time.Time) (map[string][]plans.QuotaWindow, error)
}

// Compute builds the basis at now from scratch.
func Compute(ctx context.Context, in Inputs, now time.Time) (Basis, error) {
	if in.Units == nil || in.Turns == nil {
		return Basis{}, errors.New("work quote: no transcripts or turns to compute from")
	}
	since := now.Add(-quote.Lookback)
	units, err := in.Units(ctx, since, now)
	if err != nil && len(units) == 0 {
		return Basis{}, err
	}
	turns, err := in.Turns(ctx, now.Add(-rateLookback))
	if err != nil {
		return Basis{}, err
	}
	out := Basis{Version: Version, ComputedAt: now.UTC(), Samples: Samples(units, turns, since)}
	if in.Windows != nil {
		ws, err := in.Windows(ctx, now.Add(-rateLookback), now)
		if err != nil {
			return Basis{}, err
		}
		out.Rates = Rates(ws, turns, now.Add(-quote.Lookback), now)
	}
	return out, nil
}

// harnessProvider is whose plan or bill each client's turns go to, where
// the client does not record it per turn.
var harnessProvider = map[string]string{"claude-code": "anthropic", "codex": "openai"}

// Samples prices each of the operator's own instructions since the cut-off
// from the turns of its session between its start and its last activity,
// and tells its kind of work from its words (or the facts kept for them),
// falling back to the kind in flight when the words only continue it. An
// instruction no priced turn answered is left out: it has no cost to
// learn from.
func Samples(units []agentdx.Unit, turns []analytics.SessionTurn, since time.Time) []quote.Sample {
	bySession := map[string][]analytics.SessionTurn{}
	for _, t := range turns {
		if t.Priced {
			bySession[t.SessionID] = append(bySession[t.SessionID], t)
		}
	}
	for _, ts := range bySession {
		sort.Slice(ts, func(i, j int) bool { return ts[i].At.Before(ts[j].At) })
	}
	sessions := map[string][]agentdx.Unit{}
	var order []string
	for _, u := range units {
		if _, seen := sessions[u.SessionID]; !seen {
			order = append(order, u.SessionID)
		}
		sessions[u.SessionID] = append(sessions[u.SessionID], u)
	}
	var out []quote.Sample
	for _, id := range order {
		us := sessions[id]
		sort.SliceStable(us, func(i, j int) bool { return us[i].Start.Before(us[j].Start) })
		kind := taskclass.KindUnknown
		for _, u := range us {
			kind = kindOf(u, kind)
			if agentdx.RoleOf(u.Role, u.Model) != agentdx.RolePrimary || u.Turns == 0 || u.Model == "" || u.Start.Before(since) {
				continue
			}
			usd, tokens, provider := price(bySession[id], u)
			if usd <= 0 {
				continue
			}
			s := quote.Sample{Client: u.Harness, Provider: u.Provider, Model: u.Model, Effort: u.Effort,
				At: u.Start.UTC(), USD: usd, Tokens: tokens}
			if s.Provider == "" {
				s.Provider = harnessProvider[u.Harness]
			}
			if s.Provider == "" {
				s.Provider = provider
			}
			if kind != taskclass.KindUnknown {
				s.Kind = string(kind)
			}
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// kindOf is the unit's kind of work: what its words start, or the kind in
// flight when they only continue it.
func kindOf(u agentdx.Unit, prev taskclass.Kind) taskclass.Kind {
	if u.PromptFacts.Known {
		if u.PromptFacts.Kind != "" {
			return taskclass.Kind(u.PromptFacts.Kind)
		}
		return prev
	}
	return taskclass.KindForTurn(u.Prompt, prev)
}

// price sums the session's turns between the unit's start and its last
// activity: their list price, the tokens they processed, and the provider
// most of them went to.
func price(turns []analytics.SessionTurn, u agentdx.Unit) (float64, int64, string) {
	end := u.LastActivity
	if end.IsZero() {
		end = u.End
	}
	lo := sort.Search(len(turns), func(i int) bool { return !turns[i].At.Before(u.Start) })
	var (
		usd    float64
		tokens int64
	)
	by := map[string]int{}
	for _, t := range turns[lo:] {
		if t.At.After(end) {
			break
		}
		usd += t.APIEquivalentUSD
		n := t.InputTokens + t.OutputTokens
		if n == 0 {
			n = t.Tokens
		}
		tokens += n
		by[t.Provider]++
	}
	best, most := "", 0
	for p, n := range by {
		if n > most || (n == most && p < best) {
			best, most = p, n
		}
	}
	return usd, tokens, best
}

// Rates measures each provider's window kinds' list value per percent
// from the plan-covered turns made while each window was open.
func Rates(windows map[string][]plans.QuotaWindow, turns []analytics.SessionTurn, since, now time.Time) []plans.ValueRate {
	type point struct {
		at  time.Time
		sum float64
	}
	cumulative := map[string][]point{}
	covered := make([]analytics.SessionTurn, 0, len(turns))
	for _, t := range turns {
		if t.PlanCovered && t.Priced {
			covered = append(covered, t)
		}
	}
	sort.Slice(covered, func(i, j int) bool { return covered[i].At.Before(covered[j].At) })
	for _, t := range covered {
		ps := cumulative[t.Provider]
		prev := 0.0
		if len(ps) > 0 {
			prev = ps[len(ps)-1].sum
		}
		cumulative[t.Provider] = append(ps, point{at: t.At, sum: prev + t.APIEquivalentUSD})
	}
	providers := make([]string, 0, len(windows))
	for p := range windows {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	var out []plans.ValueRate
	for _, p := range providers {
		ps := cumulative[p]
		upTo := func(at time.Time) float64 { // sum of turns at or before at
			i := sort.Search(len(ps), func(i int) bool { return ps[i].at.After(at) })
			if i == 0 {
				return 0
			}
			return ps[i-1].sum
		}
		valueIn := func(from, to time.Time) float64 { return upTo(to) - upTo(from) }
		out = append(out, plans.ValueRates(p, windows[p], since, now, valueIn)...)
	}
	return out
}

// Log is the event log the basis follows.
type Log interface {
	Position(ctx context.Context) (sqlite.Position, error)
}

// Refresh brings the basis in dir up to date at now: it is computed again
// only when the event log has moved since, or the stored one is of
// another version. It reports whether it computed.
func Refresh(ctx context.Context, log Log, in Inputs, dir string, now time.Time) (Basis, bool, error) {
	if log == nil {
		return Basis{}, false, errors.New("work quote: no event log to follow")
	}
	pos, err := log.Position(ctx)
	if err != nil {
		return Basis{}, false, err
	}
	if old, _ := Read(dir); old != nil && old.Version == Version && old.Position == pos {
		return *old, false, nil
	}
	b, err := Compute(ctx, in, now)
	if err != nil {
		return Basis{}, false, err
	}
	b.Position = pos
	return b, true, write(dir, b)
}

// DefaultDir is ~/.tokenops/cache, beside the other derived rollups.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".tokenops", "cache")
	}
	return filepath.Join(home, ".tokenops", "cache")
}

// Read reads the basis in dir; nil when there is none.
func Read(dir string) (*Basis, error) {
	b, err := os.ReadFile(filepath.Join(dir, basisFile)) //nolint:gosec // TokenOps' own cache directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out Basis
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func write(dir string, b Basis) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	tmp, err := tempfile.Create(dir, basisFile+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, basisFile))
}

// rateFor is the measured rate of provider's window of the given length,
// for a window not scoped to one model.
func (b *Basis) rateFor(provider, name string, d time.Duration) (plans.ValueRate, bool) {
	if d <= 0 {
		d = namedLength[name]
	}
	if b == nil || d <= 0 || strings.Contains(name, "(") {
		return plans.ValueRate{}, false
	}
	for _, r := range b.Rates {
		if r.Provider == provider && r.Duration == d {
			return r, true
		}
	}
	return plans.ValueRate{}, false
}

// namedLength is the length of a window a reading named but did not time.
var namedLength = map[string]time.Duration{"5h": 5 * time.Hour, "week": 7 * 24 * time.Hour}
