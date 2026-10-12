package boundaries

import (
	"fmt"
	"strings"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	"go.klarlabs.de/tokenops/internal/config"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

// Promoted is a rule after a stage change.
type Promoted struct {
	Rule   string `json:"rule"`
	Stage  string `json:"stage"`
	Config string `json:"config"`
	Note   string `json:"note"`
}

// Promote makes the named rule bind.
func Promote(path, name string) (Promoted, error) { return setStage(path, name, domain.StageEnforce) }

// Demote puts the named rule back in shadow.
func Demote(path, name string) (Promoted, error) { return setStage(path, name, domain.StageShadow) }

// setStage moves the named rule to stage in the config file at path.
//
// The stage lives in config.yaml, beside the rule, rather than in a state
// file: a rule and whether it binds are one decision, and a config shipped
// through device management (ADR 0007) then carries enforcement with it.
// A state file would let the same config bind on one machine and only
// observe on another, with nothing in the file to say so.
func setStage(path, name string, stage domain.Stage) (Promoted, error) {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, domain.OrgPrefix) {
		return Promoted{}, actions.InputError{Err: fmt.Errorf("%s is your organisation's rule: its stage is set in the team plane, by an owner or admin", name)}
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		return Promoted{}, actions.InputError{Err: err}
	}
	found := false
	for i := range cfg.Boundaries {
		if cfg.Boundaries[i].Name == name {
			cfg.Boundaries[i].Stage = string(stage)
			found = true
		}
	}
	if !found {
		return Promoted{}, actions.InputError{Err: fmt.Errorf("no boundary named %q in %s", name, path)}
	}
	if _, err := domain.Parse(cfg.Boundaries); err != nil {
		return Promoted{}, actions.InputError{Err: err}
	}
	if err := config.WriteMutable(path, cfg); err != nil {
		return Promoted{}, err
	}
	out := Promoted{Rule: name, Stage: string(stage), Config: path}
	if stage == domain.StageEnforce {
		out.Note = "the rule now binds on the next tool call wherever its hook is installed; `tokenops boundaries demote " + name + "` puts it back in shadow"
	} else {
		out.Note = "the rule records what it would have done and binds nothing"
	}
	return out, nil
}
