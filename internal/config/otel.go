package config

import (
	"time"
)

// OTelConfig configures the optional OTLP/HTTP/JSON export. When Enabled,
// the daemon pushes derived metrics to Endpoint every Interval: plan
// windows and pace, usage and its value, session grades, the coach's
// findings and cost per commit — figures only. Events, when also set,
// forwards every envelope as a log record (redacted), which carries far
// more detail and is opt-in. Headers (e.g. tenant tokens) are sent on
// every request.
type OTelConfig struct {
	Enabled  bool              `yaml:"enabled"`
	Endpoint string            `yaml:"endpoint"`
	Headers  map[string]string `yaml:"headers"`
	// Metrics pushes the derived metrics; default true when Enabled.
	Metrics *bool `yaml:"metrics"`
	// Events also forwards every envelope; default false.
	Events bool `yaml:"events"`
	// Interval between metric pushes; default one minute.
	Interval       time.Duration `yaml:"interval"`
	ServiceName    string        `yaml:"service_name"`
	ServiceVersion string        `yaml:"service_version"`
	// Redact, when true, runs the redaction pipeline before exporting.
	// Default true; explicit false disables redaction (use with care).
	Redact *bool `yaml:"redact"`
}

// MetricsEnabled reports whether derived metrics are pushed.
func (o OTelConfig) MetricsEnabled() bool {
	return o.Enabled && (o.Metrics == nil || *o.Metrics)
}

// EventsEnabled reports whether every envelope is forwarded too.
func (o OTelConfig) EventsEnabled() bool { return o.Enabled && o.Events }

// RedactEnabled reports whether redaction should be applied to OTLP
// exports. Defaults to true when unset.
func (o OTelConfig) RedactEnabled() bool {
	if o.Redact == nil {
		return true
	}
	return *o.Redact
}
