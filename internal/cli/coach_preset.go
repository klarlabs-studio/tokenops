package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
)

func newCoachPresetCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "preset [" + strings.Join(coachcap.PresetNames(), "|") + "]",
		Short: "Set the whole coach in one choice, and wire everything it needs",
		Long: `preset sets every coach power and how much the coach says in one choice,
then makes the machine match it:

  - installs the coach, read-guard, and route-guard hooks on every agent
    installed here (Claude Code, Codex, Cursor, opencode), as far as each
    agent supports them
  - sets or restores where each agent compacts, for the context power

Presets, least to most autonomous:

` + presetHelp() + `
With no argument it lists the presets and marks the one in effect. Tuning a
single power afterwards (tokenops coach set) is fine; the coach then shows as
tuned rather than as a preset.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return listPresets(cmd.OutOrStdout())
			}
			r, err := applyPreset(args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(r)
			}
			renderCoachStatus(cmd.OutOrStdout(), r, quotaStatusLines(cmd.Context(), r))
			renderHooks(cmd.OutOrStdout(), r.Hooks)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the resulting coach as JSON")
	return cmd
}

func presetHelp() string {
	var b strings.Builder
	for _, p := range coachcap.Presets() {
		fmt.Fprintf(&b, "  %-10s %s\n", p.Name, p.Summary)
	}
	return b.String()
}

func listPresets(out io.Writer) error {
	current := ""
	if path, err := config.DefaultPath(); err == nil {
		if cfg, err := config.ReadMutable(path); err == nil {
			current = coachcap.CurrentPreset(cfg)
		}
	}
	for _, p := range coachcap.Presets() {
		mark := " "
		if p.Name == current {
			mark = "*"
		}
		fmt.Fprintf(out, "%s %-10s %s\n", mark, p.Name, p.Summary)
	}
	if current == "" {
		fmt.Fprintln(out, "\nyour coach is tuned: it matches none of these exactly")
	}
	fmt.Fprintln(out, "\nset one: tokenops coach preset <name>")
	return nil
}

// applyPreset is the one implementation of choosing a preset: the CLI,
// `tokenops init --preset`, and the tokenops_coach MCP tool all run it.
func applyPreset(name string) (coachcap.Report, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return coachcap.Report{}, err
	}
	return applyPresetAt(path, name)
}

// applyPresetAt is applyPreset on the config at path.
func applyPresetAt(path, name string) (coachcap.Report, error) {
	p, ok := coachcap.PresetByName(name)
	if !ok {
		return coachcap.Report{}, fmt.Errorf("unknown preset %q (want %s)", name, strings.Join(coachcap.PresetNames(), ", "))
	}
	if _, err := os.Stat(path); err != nil {
		return coachcap.Report{}, fmt.Errorf("no config at %s: run `tokenops init --preset %s` first", path, p.Name)
	}
	now := time.Now()
	l := coachLedger()
	applied, err := coachcap.ApplyPreset(path, l, contextLevers(), p, now)
	if err != nil {
		return coachcap.Report{}, err
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		return coachcap.Report{}, err
	}
	r := coachcap.Status(cfg, l, contextLevers(), now)
	if len(applied.Compaction) > 0 {
		r.Compaction = applied.Compaction
	}
	r.Hooks = wirePresetHooks()
	return r, nil
}

// presetClients are the agents a preset wires, each with the hooks it can
// honour: Codex has no file-read tool and Cursor cannot refuse a read, so
// neither gets read-guard.
var presetClients = []struct {
	client    string
	readGuard bool
}{
	{hookClientClaudeCode, true},
	{hookClientCodex, false},
	{hookClientCursor, false},
	{hookClientOpencode, true},
}

// wirePresetHooks installs every hook the coach needs on each agent
// installed here. Hooks follow the coach block at run time, so the same
// set serves every preset: observe records through them without speaking.
func wirePresetHooks() []coachcap.HookInstall {
	var out []coachcap.HookInstall
	for _, c := range presetClients {
		if !clientInstalled(c.client) {
			continue
		}
		var buf bytes.Buffer
		err := installClientHooks(&buf, c.client, "", true, c.readGuard, true, false, coachhook.DefaultBudgetUSD)
		h := coachcap.HookInstall{Client: c.client, Status: "installed"}
		switch {
		case err != nil:
			h.Status, h.Detail = "error", err.Error()
		case strings.Contains(buf.String(), "Already up to date") || strings.Contains(buf.String(), "already"):
			h.Status = "current"
		}
		out = append(out, h)
	}
	return out
}

// clientInstalled reports whether an agent has its configuration
// directory here. A preset wires only agents that exist, rather than
// creating config for ones that do not.
func clientInstalled(client string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	dir := map[string]string{
		hookClientClaudeCode: filepath.Join(home, ".claude"),
		hookClientCodex:      filepath.Join(home, ".codex"),
		hookClientCursor:     filepath.Join(home, ".cursor"),
	}[client]
	if client == hookClientOpencode {
		pdir, err := opencodePluginDir("")
		if err != nil {
			return false
		}
		dir = filepath.Dir(pdir)
	}
	if dir == "" {
		return false
	}
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

func renderHooks(out io.Writer, hs []coachcap.HookInstall) {
	if len(hs) == 0 {
		return
	}
	fmt.Fprintln(out, "\n  hooks:")
	for _, h := range hs {
		line := fmt.Sprintf("    %-11s %s", h.Client, h.Status)
		if h.Detail != "" {
			line += " (" + h.Detail + ")"
		}
		fmt.Fprintln(out, line)
	}
}
