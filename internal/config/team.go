package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Repository naming for the team plane: what label a repository's work is
// filed under in an upload.
const (
	// TeamRepoRemote names a repository owner/name from its origin
	// remote, falling back to its directory's name.
	TeamRepoRemote = "remote"
	// TeamRepoDirectory names it by its top-level directory's name.
	TeamRepoDirectory = "directory"
	// TeamRepoHidden files every repository's work under "hidden".
	TeamRepoHidden = "hidden"
)

// Team plane defaults.
const (
	DefaultTeamInterval = time.Hour
	MinTeamInterval     = 5 * time.Minute
	DefaultTeamDays     = 14
	MaxTeamDays         = 62
)

// TeamConfig configures uploads to a team plane (ADR 0012). Nothing is sent
// until `tokenops team join` enrols this machine; these keys only shape
// what is sent and how often. The enrolment itself (server URL, device
// token) lives in its own 0600 state file, not in config.yaml.
type TeamConfig struct {
	// Enabled, when explicitly false, pauses uploads while staying joined.
	Enabled *bool `yaml:"enabled,omitempty"`
	// Interval between uploads; default one hour, at least five minutes.
	Interval time.Duration `yaml:"interval,omitempty"`
	// Days is how many UTC days each upload recomputes and replaces;
	// default 14.
	Days int `yaml:"days,omitempty"`
	// RepoNames is remote (default), directory or hidden.
	RepoNames string `yaml:"repo_names,omitempty"`
	// StatePath overrides where the enrolment is kept, default
	// ~/.tokenops/team.json.
	StatePath string `yaml:"state_path,omitempty"`
}

// UploadsEnabled reports whether uploads may run once joined.
func (t TeamConfig) UploadsEnabled() bool { return t.Enabled == nil || *t.Enabled }

// EffectiveInterval is the upload interval with its default and floor.
func (t TeamConfig) EffectiveInterval() time.Duration {
	switch {
	case t.Interval <= 0:
		return DefaultTeamInterval
	case t.Interval < MinTeamInterval:
		return MinTeamInterval
	}
	return t.Interval
}

// EffectiveDays is the recomputed window with its default.
func (t TeamConfig) EffectiveDays() int {
	if t.Days <= 0 {
		return DefaultTeamDays
	}
	return min(t.Days, MaxTeamDays)
}

// EffectiveRepoNames is the repository naming with its default.
func (t TeamConfig) EffectiveRepoNames() string {
	if t.RepoNames == "" {
		return TeamRepoRemote
	}
	return t.RepoNames
}

// applyTeamEnv reads TOKENOPS_TEAM_ENABLED, TOKENOPS_TEAM_INTERVAL,
// TOKENOPS_TEAM_DAYS, TOKENOPS_TEAM_REPO_NAMES and TOKENOPS_TEAM_STATE.
// A malformed value is ignored, as the other overrides are, and the file's
// setting stands.
func applyTeamEnv(t *TeamConfig) {
	if v := os.Getenv("TOKENOPS_TEAM_ENABLED"); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			on := true
			t.Enabled = &on
		case "0", "false", "no", "off":
			off := false
			t.Enabled = &off
		}
	}
	if v := os.Getenv("TOKENOPS_TEAM_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			t.Interval = d
		}
	}
	if v := os.Getenv("TOKENOPS_TEAM_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			t.Days = n
		}
	}
	if v := os.Getenv("TOKENOPS_TEAM_REPO_NAMES"); v != "" {
		t.RepoNames = strings.ToLower(v)
	}
	if v := os.Getenv("TOKENOPS_TEAM_STATE"); v != "" {
		t.StatePath = v
	}
}

// Validate checks the section.
func (t TeamConfig) Validate() error {
	switch t.RepoNames {
	case "", TeamRepoRemote, TeamRepoDirectory, TeamRepoHidden:
	default:
		return fmt.Errorf("team.repo_names must be %q, %q or %q, got %q", TeamRepoRemote, TeamRepoDirectory, TeamRepoHidden, t.RepoNames)
	}
	if t.Days < 0 || t.Days > MaxTeamDays {
		return fmt.Errorf("team.days must be between 1 and %d", MaxTeamDays)
	}
	if t.Interval < 0 {
		return fmt.Errorf("team.interval must not be negative")
	}
	return nil
}
