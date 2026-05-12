package eventschema

import (
	"encoding/json"
	"maps"
)

// Clone returns a deep copy of env. Subscribers that need to mutate
// the payload (e.g. redaction transforms) MUST Clone first so the
// publisher's view stays stable.
//
// The implementation goes via JSON round-trip rather than reflective
// copying so it stays stable as new payload kinds are added — every
// payload satisfies json.Marshal/Unmarshal by construction.
func (env *Envelope) Clone() (*Envelope, error) {
	if env == nil {
		return nil, nil
	}
	cp := *env
	if env.Attributes != nil {
		cp.Attributes = maps.Clone(env.Attributes)
	}
	if env.Payload != nil {
		raw, err := json.Marshal(env.Payload)
		if err != nil {
			return nil, err
		}
		switch env.Type {
		case EventTypePrompt:
			var p PromptEvent
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			cp.Payload = &p
		case EventTypeWorkflow:
			var p WorkflowEvent
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			cp.Payload = &p
		case EventTypeOptimization:
			var p OptimizationEvent
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			cp.Payload = &p
		case EventTypeCoaching:
			var p CoachingEvent
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			cp.Payload = &p
		case EventTypeRuleSource:
			var p RuleSourceEvent
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			cp.Payload = &p
		case EventTypeRuleAnalysis:
			var p RuleAnalysisEvent
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			cp.Payload = &p
		}
	}
	return &cp, nil
}
