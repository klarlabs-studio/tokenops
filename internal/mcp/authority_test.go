package mcp

import (
	"encoding/json"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/authority"
	"go.klarlabs.de/tokenops/internal/config"
)

// The CLI and the MCP tool must give the same answer to "what can
// TokenOps do without me". They assembled it separately from five
// settings in four vocabularies before, which is precisely how two
// surfaces come to disagree about what the operator's own machine is
// allowed to do.
func TestModeToolAndCLIShareOneAnswer(t *testing.T) {
	cfg := config.Default()
	cfg.Mode = config.ModePassive
	cfg.Coaching.Delivery = "intervene"

	body := modeAuthorityPayload(cfg)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got struct {
		AnythingActs bool                  `json:"anything_acts"`
		Subsystems   []authority.Subsystem `json:"subsystems"`
		HeldBack     []string              `json:"held_back,omitempty"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.AnythingActs {
		t.Error("a passive daemon reported that something acts")
	}
	if len(got.Subsystems) < 4 {
		t.Errorf("want every control surface, got %d", len(got.Subsystems))
	}
	// An agent deciding whether to propose or to act needs the capped
	// ones named, not merely the effective rung.
	if len(got.HeldBack) == 0 {
		t.Error("a coach configured to intervene under a passive daemon was not flagged as held back")
	}
}

// The payload names the setting to change, so an agent can tell the
// operator where to go rather than guessing among five files.
func TestModeToolNamesTheSettingToChange(t *testing.T) {
	for _, s := range authority.Report(config.Default()).Subsystems {
		if s.Setting == "" {
			t.Errorf("%q does not name its setting", s.Name)
		}
	}
}
