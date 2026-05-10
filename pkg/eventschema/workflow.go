package eventschema

import "time"

// WorkflowState describes the lifecycle phase reflected in a WorkflowEvent.
type WorkflowState string

// Known workflow states.
const (
	WorkflowStateUnknown   WorkflowState = "unknown"
	WorkflowStateStarted   WorkflowState = "started"
	WorkflowStateProgress  WorkflowState = "progress"
	WorkflowStateCompleted WorkflowState = "completed"
	WorkflowStateFailed    WorkflowState = "failed"
)

// WorkflowEvent captures the state of a multi-step workflow or agent run.
// Workflows are reconstructed from the stream of PromptEvents that share a
// WorkflowID; this event provides explicit checkpoints for state, cumulative
// spend, and step counts.
type WorkflowEvent struct {
	WorkflowID string `json:"workflow_id"`
	// AgentID, when set, identifies the orchestrating agent. A workflow may
	// span multiple agents — represent that with multiple events.
	AgentID string `json:"agent_id,omitempty"`
	// ParentWorkflowID supports nested workflow trees.
	ParentWorkflowID string `json:"parent_workflow_id,omitempty"`

	State WorkflowState `json:"state"`

	// StepCount is the number of recorded steps so far (including the
	// triggering step, if any).
	StepCount int64 `json:"step_count"`

	// CumulativeInputTokens, CumulativeOutputTokens, CumulativeTotalTokens
	// roll up the workflow's token consumption.
	CumulativeInputTokens  int64 `json:"cumulative_input_tokens"`
	CumulativeOutputTokens int64 `json:"cumulative_output_tokens"`
	CumulativeTotalTokens  int64 `json:"cumulative_total_tokens"`
	// CumulativeCostUSD is the rolled-up monetary cost.
	CumulativeCostUSD float64 `json:"cumulative_cost_usd,omitempty"`

	// Duration is the wall-clock duration from workflow start to this event.
	Duration time.Duration `json:"duration_ns,omitempty"`

	// ErrorCode is populated when State is failed.
	ErrorCode string `json:"error_code,omitempty"`
}

// Type identifies this payload as a WorkflowEvent.
func (*WorkflowEvent) Type() EventType { return EventTypeWorkflow }
