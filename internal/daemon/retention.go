package daemon

import (
	"fmt"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/telemetry/retention"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// retentionPolicies translates config.retention.keep into pruner
// policies. Invalid keys/durations are errors; Validate already
// rejects them at load time.
func retentionPolicies(c config.RetentionConfig) ([]retention.Policy, error) {
	if len(c.Keep) == 0 {
		return nil, nil
	}
	out := make([]retention.Policy, 0, len(c.Keep))
	for name, raw := range c.Keep {
		et, ok := retentionEventType(name)
		if !ok {
			return nil, fmt.Errorf("retention.keep: unknown event type %q", name)
		}
		d, err := config.ParseKeepDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("retention.keep[%s]: %w", name, err)
		}
		if d <= 0 {
			continue
		}
		out = append(out, retention.Policy{EventType: et, KeepFor: d})
	}
	return out, nil
}

func retentionEventType(name string) (eventschema.EventType, bool) {
	switch name {
	case "prompt":
		return eventschema.EventTypePrompt, true
	case "workflow":
		return eventschema.EventTypeWorkflow, true
	case "optimization":
		return eventschema.EventTypeOptimization, true
	case "coaching":
		return eventschema.EventTypeCoaching, true
	case "rule_source":
		return eventschema.EventTypeRuleSource, true
	case "rule_analysis":
		return eventschema.EventTypeRuleAnalysis, true
	default:
		return "", false
	}
}
