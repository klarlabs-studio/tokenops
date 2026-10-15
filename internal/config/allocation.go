package config

import (
	"os"

	"go.klarlabs.de/tokenops/internal/contexts/spend/allocation"
)

// AllocationConfig holds the allocation rules: how work is filed under a
// project, a client or a cost centre. The rules are evaluated on this
// machine and the tags stay on it; no upload carries them.
type AllocationConfig struct {
	Rules []AllocationRule `yaml:"rules,omitempty" json:"rules,omitempty"`
}

// AllocationRule tags the work it matches. Every criterion given must
// hold; the first rule that matches and sets a key gives its value.
type AllocationRule struct {
	// Repo is a glob over the repository's origin "owner/name" and its
	// directory's name, such as "acme/*".
	Repo string `yaml:"repo,omitempty" json:"repo,omitempty"`
	// Dir is a directory glob; work under a matching directory matches.
	// A leading ~ is the home directory.
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`
	// BranchPrefix matches the branch the client recorded.
	BranchPrefix string `yaml:"branch_prefix,omitempty" json:"branch_prefix,omitempty"`
	// Harness is the client: claude-code, codex, opencode, cursor, ...
	Harness string         `yaml:"harness,omitempty" json:"harness,omitempty"`
	Tags    AllocationTags `yaml:"tags" json:"tags"`
}

// AllocationTags are the values a rule sets; empty leaves a key to a later
// rule.
type AllocationTags struct {
	Project    string `yaml:"project,omitempty" json:"project,omitempty"`
	Client     string `yaml:"client,omitempty" json:"client,omitempty"`
	CostCenter string `yaml:"cost_center,omitempty" json:"cost_center,omitempty"`
}

// AllocationRules maps the configured rules into the allocation domain,
// with a leading ~ in each directory resolved.
func (c Config) AllocationRules() []allocation.Rule {
	if len(c.Allocation.Rules) == 0 {
		return nil
	}
	home, _ := os.UserHomeDir()
	out := make([]allocation.Rule, 0, len(c.Allocation.Rules))
	for _, r := range c.Allocation.Rules {
		dir := r.Dir
		if home != "" {
			dir = expandHome(dir, home)
		}
		out = append(out, allocation.Rule{
			Match: allocation.Match{Repo: r.Repo, Dir: dir, BranchPrefix: r.BranchPrefix, Harness: r.Harness},
			Tags:  allocation.Tags{Project: r.Tags.Project, Client: r.Tags.Client, CostCenter: r.Tags.CostCenter},
		})
	}
	return out
}

// Validate checks every rule.
func (a AllocationConfig) Validate() error {
	return allocation.Validate(Config{Allocation: a}.AllocationRules())
}
