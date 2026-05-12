package domainevents

import "time"

// Canonical domain event kinds. Subsystems publish via the bus; cross-
// context coordination subscribes to these strings, never to internal
// package types. Adding a new kind requires updating
// docs/telemetry-contracts.md (ubiquitous language section).
const (
	KindWorkflowStarted     = "workflow.started"
	KindWorkflowObserved    = "workflow.observed"
	KindWorkflowProgressed  = "workflow.progressed"
	KindWorkflowCompleted   = "workflow.completed"
	KindWorkflowFailed      = "workflow.failed"
	KindOptimizationApplied = "optimization.applied"
	KindRuleCorpusReloaded  = "rule_corpus.reloaded"
	KindBudgetExceeded      = "budget.exceeded"
)

// WorkflowStarted fires when a workflow's first envelope is observed.
type WorkflowStarted struct {
	WorkflowID string
	AgentID    string
	At         time.Time
}

// Kind satisfies the Event interface.
func (WorkflowStarted) Kind() string { return KindWorkflowStarted }

// WorkflowObserved fires when an offline reconstruction (replay,
// coaching, dashboard drill-down) reads a workflow trace. Distinct from
// WorkflowStarted because the workflow itself is not transitioning state
// — only being observed by a tool. Subscribers that drive live action
// (alerting, audit) should listen to WorkflowStarted; subscribers that
// just count tool usage may listen to WorkflowObserved.
type WorkflowObserved struct {
	WorkflowID string
	StepCount  int64
	At         time.Time
}

// Kind satisfies the Event interface.
func (WorkflowObserved) Kind() string { return KindWorkflowObserved }

// WorkflowCompleted fires when a workflow transitions to a terminal state.
type WorkflowCompleted struct {
	WorkflowID string
	StepCount  int64
	At         time.Time
}

// Kind satisfies the Event interface.
func (WorkflowCompleted) Kind() string { return KindWorkflowCompleted }

// OptimizationApplied fires when the optimizer pipeline commits a
// recommendation to a live request (not replay mode). OptimizerKind
// is the same identifier eventschema.OptimizationType carries
// ("prompt_compress", "semantic_dedupe", ...). The redundant naming
// avoids a circular import between domainevents and eventschema;
// values MUST match eventschema.OptimizationType members.
type OptimizationApplied struct {
	PromptHash    string
	OptimizerKind string
	TokensSaved   int64
	At            time.Time
}

// Kind satisfies the Event interface.
func (OptimizationApplied) Kind() string { return KindOptimizationApplied }

// RuleCorpusReloaded fires when the rules watcher detects a corpus
// change and the in-memory snapshot has been refreshed.
type RuleCorpusReloaded struct {
	SourceCount int
	TotalTokens int64
	At          time.Time
}

// Kind satisfies the Event interface.
func (RuleCorpusReloaded) Kind() string { return KindRuleCorpusReloaded }

// BudgetExceeded fires when a spend budget threshold is breached.
type BudgetExceeded struct {
	BudgetID string
	SpentUSD float64
	LimitUSD float64
	At       time.Time
}

// Kind satisfies the Event interface.
func (BudgetExceeded) Kind() string { return KindBudgetExceeded }
