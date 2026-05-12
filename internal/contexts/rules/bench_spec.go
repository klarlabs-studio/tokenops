package rules

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// BenchSpec is the data-transfer object (DTO) consumed by both
// `tokenops rules bench` (CLI) and `tokenops_rules_bench` (MCP). It is
// not a domain aggregate — RunBenchSpec translates it into the
// domain-side Profile / Scenario / Exposure values via the Ingestor.
// Adapters parse this DTO; the domain never sees raw paths or untyped
// signals.
type BenchSpec struct {
	Profiles  []BenchSpecProfile  `yaml:"profiles" json:"profiles"`
	Scenarios []BenchSpecScenario `yaml:"scenarios" json:"scenarios"`
}

// BenchSpecProfile is the spec form of Profile — it carries a Root path
// the loader expands into RuleDocuments rather than passing docs inline.
type BenchSpecProfile struct {
	Name     string  `yaml:"name" json:"name"`
	Root     string  `yaml:"root" json:"root"`
	RepoID   string  `yaml:"repo_id" json:"repo_id"`
	MinScore float64 `yaml:"min_score" json:"min_score"`
	Budget   int64   `yaml:"token_budget" json:"token_budget"`
	Global   bool    `yaml:"include_global" json:"include_global"`
}

// BenchSpecScenario is the spec form of Scenario.
type BenchSpecScenario struct {
	Name     string            `yaml:"name" json:"name"`
	RepoID   string            `yaml:"repo_id" json:"repo_id"`
	Workflow string            `yaml:"workflow" json:"workflow"`
	Agent    string            `yaml:"agent" json:"agent"`
	Files    []string          `yaml:"files" json:"files"`
	Tools    []string          `yaml:"tools" json:"tools"`
	Keywords []string          `yaml:"keywords" json:"keywords"`
	Exposure BenchSpecExposure `yaml:"exposure" json:"exposure"`
}

// BenchSpecExposure mirrors the downstream observations the ROI engine
// requires.
type BenchSpecExposure struct {
	Requests             int64 `yaml:"requests" json:"requests"`
	OutputTokens         int64 `yaml:"output_tokens" json:"output_tokens"`
	BaselineOutputTokens int64 `yaml:"baseline_output_tokens" json:"baseline_output_tokens"`
	Retries              int64 `yaml:"retries" json:"retries"`
}

// ParseBenchSpec decodes data as JSON first, falling back to YAML. The
// decoder is order-independent — CLI accepts either format from --spec
// files, MCP accepts either format in spec_json arguments.
func ParseBenchSpec(data []byte) (*BenchSpec, error) {
	var spec BenchSpec
	if err := json.Unmarshal(data, &spec); err == nil && (len(spec.Profiles) > 0 || len(spec.Scenarios) > 0) {
		return &spec, nil
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("decode bench spec: %w", err)
	}
	return &spec, nil
}

// RunBenchSpec loads each profile's rule corpus from disk, runs the
// Benchmark against the scenarios, and returns the scoreboard. CLI and
// MCP both delegate here so the application logic stays in the domain.
// CorpusLoader is the function signature adapters supply so the domain
// stays free of filesystem imports. internal/infra/rulesfs.LoadCorpus
// satisfies it.
type CorpusLoader func(root, repoID string) ([]*RuleDocument, error)

// RunBenchSpec runs the spec using load to materialise each profile's
// corpus. Pass rulesfs.LoadCorpus from the composition root.
func RunBenchSpec(spec *BenchSpec, load CorpusLoader) (*BenchmarkResult, error) {
	if spec == nil {
		return nil, fmt.Errorf("bench spec is nil")
	}
	if load == nil {
		return nil, fmt.Errorf("rules: RunBenchSpec requires a CorpusLoader")
	}
	profiles := make([]Profile, 0, len(spec.Profiles))
	for _, p := range spec.Profiles {
		docs, err := load(p.Root, p.RepoID)
		if err != nil {
			return nil, fmt.Errorf("load profile %s: %w", p.Name, err)
		}
		profiles = append(profiles, Profile{
			Name: p.Name, Docs: docs,
			Router: RouterConfig{
				MinScore:           p.MinScore,
				TokenBudget:        p.Budget,
				IncludeGlobalScope: p.Global,
			},
		})
	}
	scenarios := make([]Scenario, 0, len(spec.Scenarios))
	for _, s := range spec.Scenarios {
		scenarios = append(scenarios, Scenario{
			Name: s.Name,
			Signals: SelectionSignals{
				RepoID:     s.RepoID,
				WorkflowID: s.Workflow,
				AgentID:    s.Agent,
				FilePaths:  s.Files,
				Tools:      s.Tools,
				Keywords:   s.Keywords,
			},
			Exposure: Exposure{
				Requests:             s.Exposure.Requests,
				OutputTokens:         s.Exposure.OutputTokens,
				BaselineOutputTokens: s.Exposure.BaselineOutputTokens,
				Retries:              s.Exposure.Retries,
			},
		})
	}
	return NewBenchmark(ROIConfig{}).Run(profiles, scenarios), nil
}
