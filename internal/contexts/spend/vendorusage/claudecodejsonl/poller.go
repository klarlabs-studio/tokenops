package claudecodejsonl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/biller"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/jsonltail"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/pollnow"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SourceTag identifies envelopes emitted by this poller. Consumers
// (signal_quality classifier, dashboards) read this to upgrade
// Anthropic confidence to HIGH — this is real per-turn data from
// Claude Code's own conversation record, second only to Anthropic's
// own /usage endpoint.
const SourceTag = "claude-code-jsonl"

// PollerOptions configures the JSONL scanner.
type PollerOptions struct {
	// Root is the directory containing per-project session JSONLs.
	// Empty defaults to ~/.claude/projects.
	Root string
	// Interval is the gap between scans. Defaults to 30s — JSONLs
	// update on every turn so anything sub-minute catches activity
	// quickly without thrashing the filesystem.
	Interval time.Duration
	// Logger required for non-fatal errors (parse failures, missing
	// files).
	Logger *slog.Logger
	// CostSource stamps every emitted PromptEvent. The daemon sets
	// CostSourcePlanIncluded when a flat-rate plan is bound to the
	// anthropic provider (config plans:) so subscription-covered usage
	// is never repriced at API list rates by the analytics recompute.
	// Empty means metered. It applies only to turns Anthropic bills: a
	// turn a gateway served is not covered by an Anthropic plan.
	CostSource eventschema.CostSource
	// BaseURLAt returns the endpoint Claude Code was pointed at at a
	// moment (empty for Anthropic's default), from the route history. It
	// decides who bills a turn and whether a plan covers it (ADR 0009).
	// nil means the default.
	BaseURLAt func(time.Time) string
}

// The in-memory dedup set is bounded. A duplicate message ID comes from
// the next content block of the same message, written seconds later, or
// from a recent session continued into another file, so IDs of turns older
// than seenHorizon are dropped after each scan, and the set never holds
// more than maxSeenIDs. An evicted ID that does reappear is caught by the
// store, which keeps the first row for the deterministic envelope ID — the
// same guarantee a daemon restart, which starts with an empty set, relies
// on.
const (
	seenHorizon = 7 * 24 * time.Hour
	maxSeenIDs  = 200_000
)

// Poller diffs successive scans of the JSONL tree and publishes one
// PromptEvent per newly-seen assistant turn into the events bus.
// Dedup is by Anthropic message ID kept in-memory and bounded (see
// seenHorizon); across daemon restarts the store's envelope-ID dedup
// catches any replay because envelope IDs are deterministic per
// (message_id).
type Poller struct {
	bus  events.Bus
	opts PollerOptions

	mu sync.Mutex
	// seen maps each emitted message ID to its turn's time (unix ns).
	seen      map[string]int64
	maxSeen   int
	now       func() time.Time
	publishes int64

	// tail belongs to the scan goroutine alone.
	tail jsonltail.Tail[readState]
}

// NewPoller binds the bus + options. Bus may be nil — Snapshot() then
// returns parsed turns without emitting, useful for status commands.
func NewPoller(bus events.Bus, opts PollerOptions) *Poller {
	if opts.Interval <= 0 {
		opts.Interval = 30 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Poller{
		bus:     bus,
		opts:    opts,
		seen:    make(map[string]int64),
		maxSeen: maxSeenIDs,
		now:     time.Now,
	}
}

// Run blocks until ctx is cancelled, scanning the JSONL tree on each
// tick. One immediate scan up-front so the first MCP query after
// `tokenops start` sees current activity.
func (p *Poller) Run(ctx context.Context) error {
	root, err := p.resolveRoot()
	if err != nil {
		return err
	}
	t := pollnow.NewTicker(ctx, p.opts.Interval)
	defer t.Stop()
	p.scan(ctx, root)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			p.scan(ctx, root)
		}
	}
}

// PublishCount reports envelopes emitted since boot.
func (p *Poller) PublishCount() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.publishes
}

func (p *Poller) resolveRoot() (string, error) {
	if p.opts.Root != "" {
		return p.opts.Root, nil
	}
	return DefaultRoot()
}

// scan reads only what each transcript gained since the last scan.
func (p *Poller) scan(ctx context.Context, root string) {
	files, err := FindSessionFiles(root)
	if err != nil {
		p.opts.Logger.Debug("claude-code jsonl glob failed", "root", root, "err", err)
		return
	}
	visit := p.visitor(ctx)
	p.tail.Scan(ctx, files, func(path string, r io.Reader, st *readState) (int64, error) {
		return readLines(r, projectFromPath(path), st, false, visit)
	}, func(path string, err error) {
		p.opts.Logger.Warn("claude-code jsonl read failed", "path", path, "err", err)
	})
	p.pruneSeen()
}

// pruneSeen drops message IDs no later scan is likely to meet again, then
// the oldest ones beyond the cap.
func (p *Poller) pruneSeen() {
	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := p.now().Add(-seenHorizon).UnixNano()
	for id, at := range p.seen {
		if at < cutoff {
			delete(p.seen, id)
		}
	}
	over := len(p.seen) - p.maxSeen
	if over <= 0 {
		return
	}
	ats := make([]int64, 0, len(p.seen))
	for _, at := range p.seen {
		ats = append(ats, at)
	}
	slices.Sort(ats)
	// Ties at the boundary go too: the cap is a ceiling, not a target.
	newestEvicted := ats[over-1]
	for id, at := range p.seen {
		if at <= newestEvicted {
			delete(p.seen, id)
		}
	}
}

// baseURLAt is the endpoint Claude Code was pointed at at t.
func (p *Poller) baseURLAt(t time.Time) string {
	if p.opts.BaseURLAt == nil {
		return ""
	}
	return p.opts.BaseURLAt(t)
}

// visitor publishes each turn not already seen.
func (p *Poller) visitor(ctx context.Context) func(Turn) error {
	return func(turn Turn) error {
		p.mu.Lock()
		if _, dup := p.seen[turn.MessageID]; dup {
			p.mu.Unlock()
			return nil
		}
		p.seen[turn.MessageID] = turn.Timestamp.UnixNano()
		p.mu.Unlock()
		if p.bus != nil {
			p.publishWait(ctx, newEnvelope(turn, p.opts.CostSource, p.baseURLAt(turn.Timestamp)))
		}
		return nil
	}
}

// newEnvelope wraps one Turn as a PromptEvent envelope. Claude Code's
// input_tokens excludes the cache read and the cache writes, so
// InputTokens is their sum; the read and the writes ride along as its
// portions so spend.Engine prices each at its own rate.
func newEnvelope(t Turn, costSource eventschema.CostSource, baseURL string) *eventschema.Envelope {
	provider := biller.ForClaudeCodeTurn(t.Model, baseURL)
	endpoint := biller.EndpointName(baseURL, string(eventschema.ProviderAnthropic))
	// The Anthropic plan covers only Claude turns through Anthropic's own
	// endpoint. A gateway's own model is not Anthropic's, and a Claude turn
	// through a gateway runs on an API key Anthropic bills per token.
	if provider != eventschema.ProviderAnthropic || !biller.PlanApplies(string(provider), endpoint) {
		costSource = eventschema.CostSourceMetered
	}
	inputTokens := t.InputTokens + t.CacheReadInputTokens + t.CacheCreationInputTokens
	totalTokens := inputTokens + t.OutputTokens
	// Deterministic envelope ID per message — re-scanning the same
	// JSONL on poller restart never double-counts because the store
	// dedups on this ID.
	h := sha256.Sum256([]byte("claudecode-jsonl|" + t.MessageID))
	return &eventschema.Envelope{
		ID:            "ccj-" + hex.EncodeToString(h[:8]),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     t.Timestamp,
		Source:        SourceTag,
		Attributes: map[string]string{
			// One event per ASSISTANT TURN — finer than the vendor's
			// "messages" meter (user prompts). Plan window math reads
			// this to count tokens without counting the event as a
			// message (see plans.ConsumptionInWindow).
			"granularity": "assistant_turn",
			// Where the turn went (ADR 0009): a plan covers only its
			// vendor's own endpoint.
			"endpoint": endpoint,
			// Set on the one turn per operator prompt, so the plan
			// window meter can count the vendor's "messages" unit
			// without counting every turn (see plans.countsAsMessage).
			"starts_user_message":  strconv.FormatBool(t.StartsUserMessage),
			"session_id":           t.SessionID,
			"project":              t.Project,
			"message_id":           t.MessageID,
			"service_tier":         t.ServiceTier,
			"input_uncached":       fmt.Sprintf("%d", t.InputTokens),
			"cache_read_input":     fmt.Sprintf("%d", t.CacheReadInputTokens),
			"cache_creation_input": fmt.Sprintf("%d", t.CacheCreationInputTokens),
		},
		Payload: &eventschema.PromptEvent{
			Provider:                provider,
			RequestModel:            t.Model,
			InputTokens:             inputTokens,
			CachedInputTokens:       t.CacheReadInputTokens,
			CacheWriteInputTokens:   t.CacheCreationInputTokens,
			CacheWrite1hInputTokens: t.CacheCreation1hInputTokens,
			OutputTokens:            t.OutputTokens,
			TotalTokens:             totalTokens,
			// Wall-clock for the turn, reconstructed from transcript
			// timestamps. Zero when it could not be established; the
			// scorecard treats that as unmeasured rather than as instant.
			Latency:   t.Latency,
			SessionID: t.SessionID,
			// AgentID = "claude-code:<project>" enables per-project
			// rollups via group=agent in the dashboard / analytics.
			// WorkflowID = "claude-code:<project>:<session>" so the
			// waste detector + replay treat each session as its own
			// workflow, and projects are still distinguishable when
			// agents collide on session prefix.
			AgentID:    claudeCodeAgentID(t.Project),
			WorkflowID: claudeCodeWorkflowID(t.Project, t.SessionID),
			Status:     200,
			CostSource: costSource,
		},
	}
}

// claudeCodeAgentID is the canonical agent_id stamp used across
// poller writes + downstream readers (waste detector profile
// selection, dashboard group-by). Project may be empty for legacy
// JSONLs missing a parent dir — fall back to a bare prefix so the
// agent surface stays non-NULL.
func claudeCodeAgentID(project string) string {
	if project == "" {
		return "claude-code"
	}
	return "claude-code:" + project
}

// claudeCodeWorkflowID encodes (project, session) so the waste
// detector sees distinct workflows per project. The project segment
// degrades gracefully when missing.
func claudeCodeWorkflowID(project, session string) string {
	if project == "" {
		return "claude-code:" + session
	}
	return "claude-code:" + project + ":" + session
}

// publishWait hands env to the bus and waits for room rather than letting
// it be dropped. Ingestion marks each row seen before publishing, so a
// dropped envelope is never revisited: the loss is permanent, silent, and
// reproduces identically on every restart. Waiting costs a backfill some
// wall-clock and costs the operator nothing.
func (p *Poller) publishWait(ctx context.Context, env *eventschema.Envelope) {
	if env == nil {
		return
	}
	if err := p.bus.PublishWait(ctx, env); err != nil {
		p.opts.Logger.Warn("usage event not stored", "err", err)
		return
	}
	p.mu.Lock()
	p.publishes++
	p.mu.Unlock()
}
