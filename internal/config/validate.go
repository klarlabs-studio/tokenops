package config

import (
	"errors"
	"fmt"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// Validate checks the configuration for unrecoverable errors.
func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen address must not be empty")
	}
	if err := validateAllowedHosts(c.AllowedHosts); err != nil {
		return err
	}
	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid log level %q", c.Log.Level)
	}
	if err := ValidateDelivery(c.Coaching.Delivery); err != nil {
		return err
	}
	switch strings.ToLower(c.Log.Format) {
	case "json", "text":
	default:
		return fmt.Errorf("invalid log format %q", c.Log.Format)
	}
	if c.Shutdown.Timeout <= 0 {
		return fmt.Errorf("shutdown.timeout must be positive, got %s", c.Shutdown.Timeout)
	}
	if c.OTel.Enabled && c.OTel.Endpoint == "" {
		return errors.New("otel.endpoint must be set when otel.enabled is true")
	}
	if c.OTel.Interval < 0 {
		return fmt.Errorf("otel.interval must not be negative, got %s", c.OTel.Interval)
	}
	if c.Resilience.Enabled {
		if c.Resilience.FirstByteTimeout <= 0 && c.Resilience.IdleTimeout <= 0 && c.Resilience.TotalTimeout <= 0 {
			return errors.New("resilience.enabled requires at least one positive timeout (first_byte_timeout, idle_timeout, total_timeout)")
		}
	}
	for provider, planName := range c.Plans {
		if err := plans.Validate(planName); err != nil {
			return fmt.Errorf("plans[%s]: %w", provider, err)
		}
	}
	if q := c.Optimizer.RoutingMinQuality; q < 0 || q > 1 {
		return fmt.Errorf("optimizer.routing_min_quality must be in [0,1], got %g", q)
	}
	for i, r := range c.Optimizer.RoutingRules {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("optimizer.routing_rules[%d]: %w", i, err)
		}
	}
	if err := c.Coaching.Quiet.Validate(); err != nil {
		return err
	}
	if err := c.Optimizer.SmartRouting.Validate(); err != nil {
		return err
	}
	if err := c.ModelPolicy.Policy().Validate(); err != nil {
		return err
	}
	if err := c.Money.Validate(); err != nil {
		return err
	}
	if err := c.Coach.Validate(); err != nil {
		return err
	}
	for i, l := range c.Coaching.ContextLimits {
		if l.WorkflowPrefix == "" {
			return fmt.Errorf("coaching.context_limits[%d]: workflow_prefix is required", i)
		}
		if l.MaxContextTokens < 0 || l.ContextGrowthLimitTokens < 0 || l.ContextGrowthPerStepTokens < 0 || l.CompactAtTokens < 0 ||
			l.MaxConsecutiveAgentLoops < 0 || l.SystemRedundancyMin < 0 {
			return fmt.Errorf("coaching.context_limits[%d]: thresholds must be non-negative", i)
		}
	}
	switch strings.ToLower(c.Mode) {
	case "", ModePassive, ModeActive:
	default:
		return fmt.Errorf("mode must be %q or %q, got %q", ModePassive, ModeActive, c.Mode)
	}
	for i, b := range c.Budgets {
		if err := b.Validate(); err != nil {
			return fmt.Errorf("budgets[%d]: %w", i, err)
		}
	}
	if !c.Optimizer.Mode.Valid() {
		return fmt.Errorf("optimizer.mode must be %q, %q, or %q, got %q",
			OptimizerAutomatic, OptimizerInRequest, OptimizerOff, c.Optimizer.Mode)
	}
	for provider, model := range c.PreferredModels {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("preferred_models[%s]: model must not be empty", provider)
		}
	}
	if c.Watch.Interval < 0 {
		return fmt.Errorf("watch.interval must be non-negative, got %s", c.Watch.Interval)
	}
	if c.Retention.Interval < 0 {
		return fmt.Errorf("retention.interval must be non-negative, got %s", c.Retention.Interval)
	}
	for name, raw := range c.Retention.Keep {
		if _, ok := knownRetentionTypes[name]; !ok {
			return fmt.Errorf("retention.keep: unknown event type %q", name)
		}
		if _, err := ParseKeepDuration(raw); err != nil {
			return fmt.Errorf("retention.keep[%s]: %w", name, err)
		}
	}
	for key, raw := range c.Retention.KeepBySource {
		typ, src := SplitRetentionSourceKey(key)
		if src == "" {
			return fmt.Errorf("retention.keep_by_source: empty source in key %q", key)
		}
		if _, ok := knownRetentionTypes[typ]; !ok {
			return fmt.Errorf("retention.keep_by_source[%s]: unknown event type %q", key, typ)
		}
		if _, err := ParseKeepDuration(raw); err != nil {
			return fmt.Errorf("retention.keep_by_source[%s]: %w", key, err)
		}
	}
	return nil
}
