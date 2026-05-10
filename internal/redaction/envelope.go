package redaction

import (
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// RedactEnvelope walks the typed payload and Attributes of env, replacing
// secrets with placeholders. The envelope is mutated in place; the returned
// findings list aggregates every detection across all touched fields.
//
// Only string fields that may carry user-supplied content are scanned.
// Numeric fields (token counts, latencies, costs) and enum-style fields
// (provider, state, kind) are left alone — they cannot reasonably contain
// secrets, and rewriting them would corrupt the schema.
func (r *Redactor) RedactEnvelope(env *eventschema.Envelope) []Finding {
	if env == nil {
		return nil
	}
	var all []Finding

	if env.Source != "" {
		env.Source, all = redactInto(r, env.Source, all)
	}
	for k, v := range env.Attributes {
		var fs []Finding
		env.Attributes[k], fs = r.Redact(v)
		all = append(all, fs...)
	}
	if env.Payload != nil {
		all = append(all, r.redactPayload(env.Payload)...)
	}
	return all
}

func (r *Redactor) redactPayload(p eventschema.Payload) []Finding {
	switch v := p.(type) {
	case *eventschema.PromptEvent:
		return r.redactPromptEvent(v)
	case *eventschema.WorkflowEvent:
		return r.redactWorkflowEvent(v)
	case *eventschema.OptimizationEvent:
		return r.redactOptimizationEvent(v)
	case *eventschema.CoachingEvent:
		return r.redactCoachingEvent(v)
	default:
		return nil
	}
}

func (r *Redactor) redactPromptEvent(p *eventschema.PromptEvent) []Finding {
	var all []Finding
	p.PromptHash, all = redactInto(r, p.PromptHash, all)
	p.RequestModel, all = redactInto(r, p.RequestModel, all)
	p.ResponseModel, all = redactInto(r, p.ResponseModel, all)
	p.FinishReason, all = redactInto(r, p.FinishReason, all)
	p.ErrorCode, all = redactInto(r, p.ErrorCode, all)
	p.WorkflowID, all = redactInto(r, p.WorkflowID, all)
	p.AgentID, all = redactInto(r, p.AgentID, all)
	p.SessionID, all = redactInto(r, p.SessionID, all)
	p.UserID, all = redactInto(r, p.UserID, all)
	return all
}

func (r *Redactor) redactWorkflowEvent(p *eventschema.WorkflowEvent) []Finding {
	var all []Finding
	p.WorkflowID, all = redactInto(r, p.WorkflowID, all)
	p.AgentID, all = redactInto(r, p.AgentID, all)
	p.ParentWorkflowID, all = redactInto(r, p.ParentWorkflowID, all)
	p.ErrorCode, all = redactInto(r, p.ErrorCode, all)
	return all
}

func (r *Redactor) redactOptimizationEvent(p *eventschema.OptimizationEvent) []Finding {
	var all []Finding
	p.PromptHash, all = redactInto(r, p.PromptHash, all)
	p.Reason, all = redactInto(r, p.Reason, all)
	p.WorkflowID, all = redactInto(r, p.WorkflowID, all)
	p.AgentID, all = redactInto(r, p.AgentID, all)
	return all
}

func (r *Redactor) redactCoachingEvent(p *eventschema.CoachingEvent) []Finding {
	var all []Finding
	p.SessionID, all = redactInto(r, p.SessionID, all)
	p.WorkflowID, all = redactInto(r, p.WorkflowID, all)
	p.AgentID, all = redactInto(r, p.AgentID, all)
	p.Summary, all = redactInto(r, p.Summary, all)
	p.Details, all = redactInto(r, p.Details, all)
	for k, v := range p.ReplayMetadata {
		var fs []Finding
		p.ReplayMetadata[k], fs = r.Redact(v)
		all = append(all, fs...)
	}
	return all
}

// redactInto is a small helper so call sites read as one-liners.
func redactInto(r *Redactor, s string, acc []Finding) (string, []Finding) {
	if s == "" {
		return s, acc
	}
	out, fs := r.Redact(s)
	if len(fs) > 0 {
		acc = append(acc, fs...)
	}
	return out, acc
}
