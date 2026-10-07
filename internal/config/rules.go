package config

// RulesConfig wires the Rule Intelligence subsystem (issue #12).
// Enabled gates the /api/rules/* dashboard endpoints. Root is the
// repository the daemon scans on each request — defaults to the daemon's
// working directory when unset. RepoID is an opaque identifier prepended
// to rule SourceIDs (allows cross-repo aggregation without leaking repo
// names through telemetry).
type RulesConfig struct {
	Enabled bool   `yaml:"enabled"`
	Root    string `yaml:"root"`
	RepoID  string `yaml:"repo_id"`
}
