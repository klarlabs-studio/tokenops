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
	if len(c.Keep) == 0 && len(c.KeepBySource) == 0 {
		return nil, nil
	}
	out := make([]retention.Policy, 0, len(c.Keep)+len(c.KeepBySource))
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
	// Source policies are emitted even when the window is zero. A zero
	// window prunes nothing, but the pruner still needs the policy in
	// order to exclude that source from its type's window — dropping it
	// here would let the broader rule delete the rows the operator asked
	// to keep forever.
	for key, raw := range c.KeepBySource {
		typName, src := config.SplitRetentionSourceKey(key)
		if src == "" {
			return nil, fmt.Errorf("retention.keep_by_source: empty source in key %q", key)
		}
		et, ok := retentionEventType(typName)
		if !ok {
			return nil, fmt.Errorf("retention.keep_by_source[%s]: unknown event type %q", key, typName)
		}
		d, err := config.ParseKeepDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("retention.keep_by_source[%s]: %w", key, err)
		}
		out = append(out, retention.Policy{EventType: et, Source: src, KeepFor: d})
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
