package proxy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/capability/budgets"
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/tempfile"
)

// glanceRefreshBudget bounds one background computation of the glance.
const glanceRefreshBudget = 5 * time.Minute

// glanceFresh is how long a computed glance is answered without starting a
// newer one. The menu bar reads every minute.
const glanceFresh = 30 * time.Second

// glanceDiskMaxAge is the oldest saved glance answered after a start.
const glanceDiskMaxAge = 24 * time.Hour

// glanceView is what /api/glance and /api/findings answer, computed
// together: one glance serves both.
type glanceView struct {
	Glance   *headroom.GlancePayload `json:"glance"`
	Findings findings.Report         `json:"findings"`
}

// snapshots keeps a slow computation off the request path. The glance
// replays weeks of events several times over, and the daemon runs at
// background priority: on a store with a month of heavy Codex use it takes
// longer than the menu bar waits, and a request that computed it showed
// nothing. Instead every request is answered from the last result, at
// once, while a stale one is recomputed in the background.
//
// With path set, each result is saved there, and after a start the saved
// one (under maxDiskAge) is answered until the first computation lands, so
// a restart, which an upgrade does, never blanks the menu bar. Only a
// first start with nothing saved waits.
type snapshots[T any] struct {
	compute    func(context.Context, time.Time) (T, error)
	fresh      time.Duration
	path       string
	maxDiskAge time.Duration
	// key names what a result depends on beyond time (the config): a
	// saved result under another key is not answered.
	key string

	mu sync.Mutex
	v  T
	at time.Time // the now it was computed for
	ok bool
	// loaded is whether the saved result was looked for.
	loaded bool
	// running is open while a computation runs; err is how the last one
	// ended.
	running chan struct{}
	err     error
}

// saved is a result as written to path.
type saved[T any] struct {
	At    time.Time `json:"at"`
	Key   string    `json:"key,omitempty"`
	Value T         `json:"value"`
}

// get is the result for a request at now and when it was computed: the
// snapshot when there is one, starting a newer computation when it is
// stale; otherwise it waits for the first one, or for ctx.
func (c *snapshots[T]) get(ctx context.Context, now time.Time) (T, time.Time, error) {
	c.mu.Lock()
	if !c.ok && !c.loaded {
		c.loaded = true
		c.load(now)
	}
	if c.ok && now.Sub(c.at) < c.fresh {
		v, at := c.v, c.at
		c.mu.Unlock()
		return v, at, nil
	}
	if c.running == nil {
		c.start(now)
	}
	if c.ok {
		v, at := c.v, c.at
		c.mu.Unlock()
		return v, at, nil
	}
	running := c.running
	c.mu.Unlock()

	var zero T
	select {
	case <-running:
	case <-ctx.Done():
		return zero, time.Time{}, ctx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ok {
		return zero, time.Time{}, c.err
	}
	return c.v, c.at, nil
}

// stale makes the next request start a newer computation, as after an
// action that changes what it shows; the current result is still answered
// until that one lands.
func (c *snapshots[T]) stale() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = time.Time{}
}

// start computes the result for now in the background. c.mu is held. The
// computation outlives the request that started it: it serves the next.
func (c *snapshots[T]) start(now time.Time) {
	done := make(chan struct{})
	c.running = done
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), glanceRefreshBudget)
		v, err := c.compute(ctx, now)
		cancel()
		if err == nil {
			c.save(v, now)
		}
		c.mu.Lock()
		if err == nil {
			c.v, c.at, c.ok = v, now, true
		}
		c.err, c.running = err, nil
		c.mu.Unlock()
		close(done)
	}()
}

// load answers from the saved result when there is a recent one. c.mu is
// held. A missing, unreadable or old file leaves nothing to answer.
func (c *snapshots[T]) load(now time.Time) {
	if c.path == "" {
		return
	}
	b, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	var s saved[T]
	if json.Unmarshal(b, &s) != nil || s.Key != c.key || s.At.IsZero() || s.At.After(now) || now.Sub(s.At) > c.maxDiskAge {
		return
	}
	c.v, c.at, c.ok = s.Value, s.At, true
}

// save writes v to path, replacing the file whole. A failure only means
// the next start computes before it answers.
func (c *snapshots[T]) save(v T, at time.Time) {
	if c.path == "" {
		return
	}
	b, err := json.Marshal(saved[T]{At: at, Key: c.key, Value: v})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return
	}
	tmp, err := tempfile.Create(filepath.Dir(c.path), ".glance-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), c.path) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// waitIdle returns once no computation is running, for tests.
func (c *snapshots[T]) waitIdle() {
	c.mu.Lock()
	running := c.running
	c.mu.Unlock()
	if running != nil {
		<-running
	}
}

// WithGlanceSnapshot saves each computed glance and its findings at path,
// and answers from the saved ones after a start until the first
// computation lands. key names the config they were computed under: a
// save under another (the config changed, and the daemon restarted for
// it) is not answered.
func WithGlanceSnapshot(path, key string) Option {
	return func(s *Server) { s.glancePath, s.glanceKey = path, key }
}

// WithGlanceCosts has each computed glance carry its plans' providers'
// usage today and over 30 days, read through s: the cards show them, and
// a client that asks for the glance would otherwise read 30 days of
// usage itself. A nil s leaves them out.
func WithGlanceCosts(s spending.Summarizer) Option {
	return func(srv *Server) { srv.glanceCosts = s }
}

// glanceCostsBudget bounds the cost reads of one glance. They run in the
// background, beside a glance that took longer.
const glanceCostsBudget = 30 * time.Second

// view is the glance and findings for a request at now, from the plan
// routes' deps, and when they were computed.
func (s *Server) view(ctx context.Context, now time.Time) (glanceView, time.Time, error) {
	s.glancesOnce.Do(func() {
		s.glances = &snapshots[glanceView]{compute: s.computeView, fresh: glanceFresh,
			path: s.glancePath, key: s.glanceKey, maxDiskAge: glanceDiskMaxAge}
	})
	return s.glances.get(ctx, now)
}

// computeView computes the glance and the findings ranked around it.
func (s *Server) computeView(ctx context.Context, now time.Time) (glanceView, error) {
	d := s.plans()
	g, err := headroom.ComputeGlance(ctx, d, now)
	if err != nil {
		return glanceView{}, err
	}
	p := g.Payload()
	p.Boundaries = boundaries.GlanceAdvice(d.Config, g.Headroom.Reports)
	p.SpendBudgets = budgets.Current("", now)
	p.ComputedAt = now
	if s.glanceCosts != nil {
		costCtx, cancel := context.WithTimeout(ctx, glanceCostsBudget)
		p.ProviderCosts = spending.CostsOf(costCtx, s.glanceCosts, g.Headroom.Providers(), now)
		p.ProviderCostsLate = costCtx.Err() != nil
		cancel()
	}
	var c *coachcap.Report
	if s.state != nil {
		if st := s.state(); st.Coach != nil {
			r := st.Coach(now)
			c = &r
		}
	}
	return glanceView{Glance: p, Findings: findings.Compute(findings.Gather(&g, c, d.Config, findings.DefaultDir()))}, nil
}
