package eventschema

import "time"

// DecisionStage identifies where a durable decision is in its lifecycle.
type DecisionStage string

const (
	DecisionStageShadow   DecisionStage = "shadow"
	DecisionStageProposed DecisionStage = "proposed"
	DecisionStageAccepted DecisionStage = "accepted"
	DecisionStageRejected DecisionStage = "rejected"
	DecisionStageApplied  DecisionStage = "applied"
	DecisionStageExpired  DecisionStage = "expired"
	DecisionStageFailed   DecisionStage = "failed"
)

// ResourceOption is one alternative considered by a resource decision.
type ResourceOption struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// EvidenceRef points at evidence without copying private source material into
// a decision record.
type EvidenceRef struct {
	EventID    string    `json:"event_id,omitempty"`
	Kind       string    `json:"kind"`
	Source     string    `json:"source"`
	ObservedAt time.Time `json:"observed_at"`
	FreshUntil time.Time `json:"fresh_until,omitzero"`
	Confidence float64   `json:"confidence,omitempty"`
	Scope      string    `json:"scope,omitempty"`
	Caveat     string    `json:"caveat,omitempty"`
}

// DecisionEvent preserves what TokenOps knew and why it selected an action.
// Multiple events with one Correlation.Decision form its append-only history.
type DecisionEvent struct {
	Kind         string           `json:"kind"`
	Stage        DecisionStage    `json:"stage"`
	Alternatives []ResourceOption `json:"alternatives,omitempty"`
	Selected     ResourceOption   `json:"selected,omitzero"`
	Evidence     []EvidenceRef    `json:"evidence,omitempty"`
	Policy       string           `json:"policy,omitempty"`
	Authority    string           `json:"authority,omitempty"`
	Confidence   float64          `json:"confidence,omitempty"`
	Rationale    string           `json:"rationale"`
	Executable   bool             `json:"executable"`
	Adapter      string           `json:"adapter,omitempty"`
	Fingerprint  string           `json:"fingerprint,omitempty"`
}

// Type identifies this payload as a DecisionEvent.
func (*DecisionEvent) Type() EventType { return EventTypeDecision }

// OutcomeResult describes whether an execution accomplished its goal.
type OutcomeResult string

const (
	OutcomeUnknown     OutcomeResult = "unknown"
	OutcomeAchieved    OutcomeResult = "achieved"
	OutcomePartial     OutcomeResult = "partial"
	OutcomeNotAchieved OutcomeResult = "not_achieved"
)

// OutcomeAssessment records the strength of the outcome evidence.
type OutcomeAssessment string

const (
	OutcomeSelfReport   OutcomeAssessment = "self_report"
	OutcomeVerification OutcomeAssessment = "verification"
	OutcomeHuman        OutcomeAssessment = "human"
)

// OutcomeMetric is a provenance-carrying result measurement.
type OutcomeMetric struct {
	Name       string    `json:"name"`
	Unit       string    `json:"unit"`
	Value      float64   `json:"value"`
	Source     string    `json:"source"`
	ObservedAt time.Time `json:"observed_at"`
	Confidence float64   `json:"confidence,omitempty"`
	Caveat     string    `json:"caveat,omitempty"`
}

// OutcomeEvent records an assessment separately from execution consumption.
type OutcomeEvent struct {
	Result     OutcomeResult     `json:"result"`
	Assessment OutcomeAssessment `json:"assessment,omitempty"`
	Caveat     string            `json:"caveat,omitempty"`
	Evidence   []EvidenceRef     `json:"evidence,omitempty"`
	Metrics    []OutcomeMetric   `json:"metrics,omitempty"`
}

// Type identifies this payload as an OutcomeEvent.
func (*OutcomeEvent) Type() EventType { return EventTypeOutcome }

// ExperimentStage identifies the lifecycle of a bounded local experiment.
type ExperimentStage string

const (
	ExperimentStarted   ExperimentStage = "started"
	ExperimentAssigned  ExperimentStage = "assigned"
	ExperimentPaused    ExperimentStage = "paused"
	ExperimentCompleted ExperimentStage = "completed"
	ExperimentStopped   ExperimentStage = "stopped"
)

// ExperimentEvent records consent, bounds and assignments for a local trial.
type ExperimentEvent struct {
	Stage       ExperimentStage `json:"stage"`
	Kind        string          `json:"kind"`
	Baseline    ResourceOption  `json:"baseline,omitzero"`
	Variant     ResourceOption  `json:"variant,omitzero"`
	Assignment  string          `json:"assignment,omitempty"`
	Pair        int             `json:"pair,omitempty"`
	MaxPairs    int             `json:"max_pairs,omitempty"`
	EndsAt      time.Time       `json:"ends_at,omitzero"`
	Reason      string          `json:"reason,omitempty"`
	Fingerprint string          `json:"fingerprint,omitempty"`
	// ObjectiveMetric and MinImprovementPct declare the primary, lower-is-better
	// metric and minimum per-pair improvement required for promotion.
	ObjectiveMetric   string                `json:"objective_metric,omitempty"`
	MinImprovementPct float64               `json:"min_improvement_pct,omitempty"`
	Guardrails        []ExperimentGuardrail `json:"guardrails,omitempty"`
}

// ExperimentGuardrail declares a measured metric that must not regress beyond
// MaxRegressionPct. The special metric "quality" is non-inferiority on the
// independently assessed outcome and must use a zero threshold.
type ExperimentGuardrail struct {
	Metric           string  `json:"metric"`
	MaxRegressionPct float64 `json:"max_regression_pct"`
}

// Type identifies this payload as an ExperimentEvent.
func (*ExperimentEvent) Type() EventType { return EventTypeExperiment }
