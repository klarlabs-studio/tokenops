package config

import (
	"testing"
	"time"
)

func TestTeamDefaults(t *testing.T) {
	var tc TeamConfig
	if !tc.UploadsEnabled() || tc.EffectiveInterval() != time.Hour || tc.EffectiveDays() != 14 || tc.EffectiveRepoNames() != TeamRepoRemote {
		t.Errorf("defaults: %+v", tc)
	}
	tc.Interval = time.Second
	if tc.EffectiveInterval() != MinTeamInterval {
		t.Error("interval below the floor was kept")
	}
	if err := (TeamConfig{RepoNames: "paths"}).Validate(); err == nil {
		t.Error("repo_names=paths accepted")
	}
}

func TestTeamEnv(t *testing.T) {
	t.Setenv("TOKENOPS_TEAM_ENABLED", "off")
	t.Setenv("TOKENOPS_TEAM_INTERVAL", "30m")
	t.Setenv("TOKENOPS_TEAM_DAYS", "7")
	t.Setenv("TOKENOPS_TEAM_REPO_NAMES", "Hidden")
	t.Setenv("TOKENOPS_TEAM_STATE", "/tmp/team.json")
	var tc TeamConfig
	applyTeamEnv(&tc)
	if tc.UploadsEnabled() || tc.Interval != 30*time.Minute || tc.Days != 7 || tc.RepoNames != TeamRepoHidden || tc.StatePath != "/tmp/team.json" {
		t.Errorf("env: %+v", tc)
	}
}
