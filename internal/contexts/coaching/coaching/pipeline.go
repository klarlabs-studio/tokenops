// Package coaching wires the replay engine to the waste detector to
// emit asynchronous coaching events for past sessions. The async
// pipeline is what the dashboard surfaces as "5 weekly insights" — it
// runs off the request hot path on whatever cadence the operator
// configures (cron, on-demand, idle worker).
//
// The pipeline is intentionally small: a buffered job queue, a worker
// pool, a per-run cost budget that pauses dispatch when exceeded, and
// a sink interface for delivered coaching envelopes (sqlite, Kafka,
// Slack — the package does not assume).
package coaching

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/felixgeelhaar/tokenops/internal/contexts/coaching/waste"
	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/replay"
	"github.com/felixgeelhaar/tokenops/internal/contexts/workflows/workflow"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Job describes one unit of coaching work: replay a session, detect
// waste, emit coaching envelopes.
type Job struct {
	WorkflowID string
	// CostEstimateUSD is the operator's projected cost of running this
	// job. The pipeline accumulates it; jobs that would push the
	// running total over Config.CostBudgetUSD are skipped (re-queued
	// for the next budget cycle).
	CostEstimateUSD float64
}

// Sink receives coaching envelopes the pipeline produces. The sqlite
// store + audit recorder both satisfy this trivially.
type Sink interface {
	Publish(env *eventschema.Envelope)
}

// FuncSink adapts a func to Sink — useful for tests and tiny callers.
type FuncSink func(env *eventschema.Envelope)

// Publish delegates to the wrapped function.
func (f FuncSink) Publish(env *eventschema.Envelope) { f(env) }

// SummaryEnricher upgrades a heuristic-generated CoachingEvent with a
// natural-language Summary. Pipeline calls it for every emitted event
// when set; nil leaves the heuristic Summary in place. Errors are
// logged + swallowed — the original event still ships.
type SummaryEnricher interface {
	Enrich(ctx context.Context, ev *eventschema.CoachingEvent) error
}

// Config tunes the pipeline.
type Config struct {
	// Concurrency is the worker-pool size. Default 2.
	Concurrency int
	// QueueCapacity caps queued Jobs. Submit blocks when full.
	// Default 64.
	QueueCapacity int
	// CostBudgetUSD pauses dispatch once total CostEstimateUSD across
	// processed jobs reaches this value. Zero disables the cap.
	CostBudgetUSD float64
	// Source identifies the emitter on every envelope (default "coaching").
	Source string
	// Logger receives lifecycle + error logs.
	Logger *slog.Logger
	// Enricher, when set, runs after the detector emits a
	// CoachingEvent and is allowed to upgrade Summary / Details with
	// LLM-generated text. Wired by the daemon from the configured
	// coaching LLM backend; tests inject fakes.
	Enricher SummaryEnricher
}

func (c *Config) defaults() {
	if c.Concurrency <= 0 {
		c.Concurrency = 2
	}
	if c.QueueCapacity <= 0 {
		c.QueueCapacity = 64
	}
	if c.Source == "" {
		c.Source = "coaching"
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// Pipeline runs replay → waste → sink for queued jobs.
type Pipeline struct {
	cfg      Config
	replay   *replay.Engine
	detector *waste.Detector
	sink     Sink

	queue chan Job
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once

	processed atomic.Int64
	skipped   atomic.Int64
	emitted   atomic.Int64
	costSpent atomic.Uint64 // millicents to keep atomic int64
}

// New constructs a Pipeline. replay + detector + sink are required; nil
// values produce ErrInvalidConfig from Start.
func New(cfg Config, replay *replay.Engine, detector *waste.Detector, sink Sink) *Pipeline {
	cfg.defaults()
	return &Pipeline{
		cfg:      cfg,
		replay:   replay,
		detector: detector,
		sink:     sink,
		queue:    make(chan Job, cfg.QueueCapacity),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// ErrInvalidConfig surfaces missing dependencies.
var ErrInvalidConfig = errors.New("coaching: missing replay/detector/sink")

// Start spawns the worker pool. Blocks until ctx is cancelled or Close
// is called. Returns ErrInvalidConfig immediately when constructed
// without a replay engine, detector, or sink.
func (p *Pipeline) Start(ctx context.Context) error {
	if p.replay == nil || p.detector == nil || p.sink == nil {
		return ErrInvalidConfig
	}
	var wg sync.WaitGroup
	wg.Add(p.cfg.Concurrency)
	for i := 0; i < p.cfg.Concurrency; i++ {
		go func(id int) {
			defer wg.Done()
			p.workerLoop(ctx, id)
		}(i)
	}
	wg.Wait()
	close(p.done)
	return nil
}

// Submit enqueues job. Blocks until queue capacity is available or
// ctx / Close fires; ctx-cancellation returns its error, Close returns
// ErrClosed.
func (p *Pipeline) Submit(ctx context.Context, job Job) error {
	select {
	case <-p.stop:
		return ErrClosed
	default:
	}
	select {
	case p.queue <- job:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.stop:
		return ErrClosed
	}
}

// Close signals workers to stop after draining the queue. Idempotent.
func (p *Pipeline) Close() {
	p.once.Do(func() { close(p.stop) })
}

// Wait blocks until Start returns.
func (p *Pipeline) Wait() { <-p.done }

// Stats returns the running counters. Safe to call any time.
func (p *Pipeline) Stats() Stats {
	return Stats{
		Processed: p.processed.Load(),
		Skipped:   p.skipped.Load(),
		Emitted:   p.emitted.Load(),
		CostSpent: float64(p.costSpent.Load()) / 1e5,
	}
}

// Stats summarises pipeline counters.
type Stats struct {
	Processed int64
	Skipped   int64
	Emitted   int64
	CostSpent float64
}

// ErrClosed is returned by Submit after Close.
var ErrClosed = errors.New("coaching: pipeline closed")

func (p *Pipeline) workerLoop(ctx context.Context, id int) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stop:
			return
		case job, ok := <-p.queue:
			if !ok {
				return
			}
			if !p.charge(job.CostEstimateUSD) {
				p.skipped.Add(1)
				p.cfg.Logger.Warn("coaching skipped: cost budget exhausted",
					"worker", id, "workflow_id", job.WorkflowID,
					"cost_estimate", job.CostEstimateUSD)
				continue
			}
			p.process(ctx, job)
		}
	}
}

func (p *Pipeline) charge(estimate float64) bool {
	if p.cfg.CostBudgetUSD <= 0 {
		return true
	}
	delta := uint64(estimate * 1e5)
	for {
		cur := p.costSpent.Load()
		next := cur + delta
		if float64(next)/1e5 > p.cfg.CostBudgetUSD {
			return false
		}
		if p.costSpent.CompareAndSwap(cur, next) {
			return true
		}
	}
}

func (p *Pipeline) process(ctx context.Context, job Job) {
	p.processed.Add(1)
	res, err := p.replay.Replay(ctx, replay.SessionSelector{WorkflowID: job.WorkflowID})
	if err != nil {
		if errors.Is(err, replay.ErrEmptySession) {
			return
		}
		p.cfg.Logger.Error("coaching replay error",
			"workflow_id", job.WorkflowID, "err", err)
		return
	}
	trace := tracePlaceholder(job.WorkflowID, res)
	for _, ev := range p.detector.Detect(trace) {
		if p.cfg.Enricher != nil {
			if err := p.cfg.Enricher.Enrich(ctx, ev); err != nil {
				p.cfg.Logger.Warn("coaching enrichment failed",
					"workflow_id", job.WorkflowID, "err", err)
				// keep heuristic Summary; the event still ships
			}
		}
		env := &eventschema.Envelope{
			ID:            uuid.NewString(),
			SchemaVersion: eventschema.SchemaVersion,
			Type:          eventschema.EventTypeCoaching,
			Timestamp:     time.Now().UTC(),
			Source:        p.cfg.Source,
			Payload:       ev,
		}
		p.sink.Publish(env)
		p.emitted.Add(1)
	}
}

// tracePlaceholder builds a minimal workflow.Trace from the replay
// result so the waste detector can score it without the caller wiring
// internal/workflow.Reconstruct twice. Fields not surfaced by the
// replay result (StartedAt, Models map, etc.) are derived from the
// underlying envelopes.
func tracePlaceholder(workflowID string, res *replay.Result) *workflow.Trace {
	if res == nil || len(res.Steps) == 0 {
		return &workflow.Trace{WorkflowID: workflowID}
	}
	t := &workflow.Trace{
		WorkflowID: workflowID,
		Steps:      make([]workflow.Step, len(res.Steps)),
		StepCount:  len(res.Steps),
		Models:     map[string]int{},
		Agents:     map[string]int{},
		StartedAt:  res.Steps[0].OriginalEnvelope.Timestamp,
		EndedAt:    res.Steps[len(res.Steps)-1].OriginalEnvelope.Timestamp,
	}
	t.Duration = t.EndedAt.Sub(t.StartedAt)
	var prevInput int64
	for i, s := range res.Steps {
		pe, ok := s.OriginalEnvelope.Payload.(*eventschema.PromptEvent)
		if !ok {
			continue
		}
		step := workflow.Step{
			Index: i, Envelope: s.OriginalEnvelope, Prompt: pe,
		}
		if i > 0 {
			step.ContextDelta = pe.InputTokens - prevInput
			step.StartGap = s.OriginalEnvelope.Timestamp.Sub(res.Steps[i-1].OriginalEnvelope.Timestamp)
		}
		t.Steps[i] = step
		t.TotalInputTokens += pe.InputTokens
		t.TotalOutputTokens += pe.OutputTokens
		t.TotalTotalTokens += pe.TotalTokens
		t.TotalCostUSD += pe.CostUSD
		if pe.InputTokens > t.MaxContextSize {
			t.MaxContextSize = pe.InputTokens
		}
		if step.ContextDelta > 0 {
			t.ContextGrowthTotal += step.ContextDelta
		}
		if pe.RequestModel != "" {
			t.Models[pe.RequestModel]++
		}
		if pe.AgentID != "" {
			t.Agents[pe.AgentID]++
		}
		prevInput = pe.InputTokens
	}
	return t
}

// Diagnostic helper: render Stats as a string.
func (s Stats) String() string {
	return fmt.Sprintf("processed=%d skipped=%d emitted=%d cost_spent=$%.2f",
		s.Processed, s.Skipped, s.Emitted, s.CostSpent)
}
