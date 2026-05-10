package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// row is the indexed projection of an Envelope persisted in the events table.
// Fields lifted out of the payload exist for query-time filtering; the full
// payload remains available as JSON.
type row struct {
	ID            string
	SchemaVersion string
	Type          eventschema.EventType
	TimestampNS   int64
	Day           int64
	TraceID       sql.NullString
	SpanID        sql.NullString
	Source        sql.NullString
	Provider      sql.NullString
	Model         sql.NullString
	WorkflowID    sql.NullString
	AgentID       sql.NullString
	SessionID     sql.NullString
	UserID        sql.NullString
	InputTokens   sql.NullInt64
	OutputTokens  sql.NullInt64
	TotalTokens   sql.NullInt64
	CostUSD       sql.NullFloat64
	Payload       string
	Attributes    sql.NullString
}

// secondsPerDay is used to bucket timestamps into UTC-day partitions for the
// (day, type) index. SQLite has no DATE type; an integer day is enough.
const secondsPerDay = 86_400

func envelopeToRow(env *eventschema.Envelope) (row, error) {
	if env == nil {
		return row{}, fmt.Errorf("envelope is nil")
	}
	if env.ID == "" {
		return row{}, fmt.Errorf("envelope id is empty")
	}
	if env.Type == "" {
		return row{}, fmt.Errorf("envelope type is empty")
	}
	if env.Payload == nil {
		return row{}, fmt.Errorf("envelope payload is nil")
	}
	if env.Type != env.Payload.Type() {
		return row{}, fmt.Errorf("envelope type %q does not match payload type %q",
			env.Type, env.Payload.Type())
	}

	payloadJSON, err := json.Marshal(env.Payload)
	if err != nil {
		return row{}, fmt.Errorf("marshal payload: %w", err)
	}

	r := row{
		ID:            env.ID,
		SchemaVersion: env.SchemaVersion,
		Type:          env.Type,
		TimestampNS:   env.Timestamp.UTC().UnixNano(),
		Day:           env.Timestamp.UTC().Unix() / secondsPerDay,
		TraceID:       nullString(env.TraceID),
		SpanID:        nullString(env.SpanID),
		Source:        nullString(env.Source),
		Payload:       string(payloadJSON),
	}
	if len(env.Attributes) > 0 {
		attrJSON, err := json.Marshal(env.Attributes)
		if err != nil {
			return row{}, fmt.Errorf("marshal attributes: %w", err)
		}
		r.Attributes = sql.NullString{String: string(attrJSON), Valid: true}
	}

	switch p := env.Payload.(type) {
	case *eventschema.PromptEvent:
		r.Provider = nullString(string(p.Provider))
		r.Model = nullString(p.RequestModel)
		r.WorkflowID = nullString(p.WorkflowID)
		r.AgentID = nullString(p.AgentID)
		r.SessionID = nullString(p.SessionID)
		r.UserID = nullString(p.UserID)
		r.InputTokens = nullInt64IfNonZero(p.InputTokens)
		r.OutputTokens = nullInt64IfNonZero(p.OutputTokens)
		r.TotalTokens = nullInt64IfNonZero(p.TotalTokens)
		r.CostUSD = nullFloat64IfNonZero(p.CostUSD)
	case *eventschema.WorkflowEvent:
		r.WorkflowID = nullString(p.WorkflowID)
		r.AgentID = nullString(p.AgentID)
		r.InputTokens = nullInt64IfNonZero(p.CumulativeInputTokens)
		r.OutputTokens = nullInt64IfNonZero(p.CumulativeOutputTokens)
		r.TotalTokens = nullInt64IfNonZero(p.CumulativeTotalTokens)
		r.CostUSD = nullFloat64IfNonZero(p.CumulativeCostUSD)
	case *eventschema.OptimizationEvent:
		r.WorkflowID = nullString(p.WorkflowID)
		r.AgentID = nullString(p.AgentID)
	case *eventschema.CoachingEvent:
		r.WorkflowID = nullString(p.WorkflowID)
		r.AgentID = nullString(p.AgentID)
		r.SessionID = nullString(p.SessionID)
	default:
		return row{}, fmt.Errorf("unsupported payload type %T", p)
	}

	return r, nil
}

func rowToEnvelope(r row) (*eventschema.Envelope, error) {
	payload, err := decodePayload(r.Type, []byte(r.Payload))
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	env := &eventschema.Envelope{
		ID:            r.ID,
		SchemaVersion: r.SchemaVersion,
		Type:          r.Type,
		Timestamp:     timeFromNS(r.TimestampNS),
		TraceID:       r.TraceID.String,
		SpanID:        r.SpanID.String,
		Source:        r.Source.String,
		Payload:       payload,
	}
	if r.Attributes.Valid {
		if err := json.Unmarshal([]byte(r.Attributes.String), &env.Attributes); err != nil {
			return nil, fmt.Errorf("decode attributes: %w", err)
		}
	}
	return env, nil
}

func decodePayload(t eventschema.EventType, raw []byte) (eventschema.Payload, error) {
	switch t {
	case eventschema.EventTypePrompt:
		var p eventschema.PromptEvent
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return &p, nil
	case eventschema.EventTypeWorkflow:
		var p eventschema.WorkflowEvent
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return &p, nil
	case eventschema.EventTypeOptimization:
		var p eventschema.OptimizationEvent
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return &p, nil
	case eventschema.EventTypeCoaching:
		var p eventschema.CoachingEvent
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return &p, nil
	default:
		return nil, fmt.Errorf("unknown event type %q", t)
	}
}
