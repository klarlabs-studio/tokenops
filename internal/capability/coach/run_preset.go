package coach

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
)

// presetTimeout bounds a preset run: config writes and a few settings
// files, nothing slow.
const presetTimeout = 30 * time.Second

// ErrPreset is a preset the CLI refused; its message is the CLI's.
var ErrPreset = errors.New("coach preset")

// RunPreset applies a preset through the CLI's own implementation, run as
// this binary, so choosing a preset from an agent, a menu bar or the
// terminal cannot wire a machine differently.
func RunPreset(ctx context.Context, name string) (Report, error) {
	exe, err := os.Executable()
	if err != nil {
		return Report{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, presetTimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, exe, "coach", "preset", name, "--json") //nolint:gosec // this binary, fixed arguments
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return Report{}, fmt.Errorf("%w: %s", ErrPreset, msg)
	}
	var r Report
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		return Report{}, fmt.Errorf("coach preset: %w", err)
	}
	return r, nil
}

// ChangeRequest sets the coach: a whole preset, or one or more dials.
type ChangeRequest struct {
	// Preset sets everything in one choice: observe, advise, guided or
	// autopilot. The other fields are ignored when it is set.
	Preset string `json:"preset,omitempty"`
	// Autonomy sets every power's default: off, advise, ask, autonomous.
	Autonomy string `json:"autonomy,omitempty"`
	// Verbosity sets how much the coach says: quiet, normal, verbose.
	Verbosity string `json:"verbosity,omitempty"`
	// Power and Rung override one power's autonomy; they go together.
	Power string `json:"power,omitempty"`
	Rung  string `json:"rung,omitempty"`
}

// Empty reports whether the request changes nothing.
func (r ChangeRequest) Empty() bool {
	return r.Preset == "" && r.Autonomy == "" && r.Verbosity == "" && r.Power == "" && r.Rung == ""
}

// ErrPowerRung is a power without a rung or a rung without a power.
var ErrPowerRung = errors.New("power and rung go together")

// Change applies req to the config at path and reports the coach after it.
func Change(ctx context.Context, path string, l Ledger, levers ContextLevers, now time.Time, req ChangeRequest) (Report, error) {
	if req.Preset != "" {
		return RunPreset(ctx, req.Preset)
	}
	if (req.Power == "") != (req.Rung == "") {
		return Report{}, ErrPowerRung
	}
	return Apply(path, l, levers, now, func(cfg *config.Config) {
		if req.Autonomy != "" {
			cfg.Coach.Autonomy = req.Autonomy
		}
		if req.Verbosity != "" {
			cfg.Coach.Verbosity = req.Verbosity
		}
		if req.Power != "" {
			if cfg.Coach.Powers == nil {
				cfg.Coach.Powers = map[string]string{}
			}
			cfg.Coach.Powers[strings.ToLower(strings.TrimSpace(req.Power))] = req.Rung
		}
	})
}
