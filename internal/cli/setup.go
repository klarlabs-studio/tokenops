package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"go.klarlabs.de/tokenops/internal/cli/detect"
	"go.klarlabs.de/tokenops/internal/cli/wire"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
)

// setupStep is one thing init did (or deliberately did not do), so the
// closing summary can be specific instead of claiming blanket success.
type setupStep struct {
	Name string
	// Detail is what happened, in the operator's terms.
	Detail string
	// Changed distinguishes work done from state already correct —
	// re-running init should visibly be a no-op.
	Changed bool
	// Manual marks a step init deliberately left to the operator, with
	// Detail explaining why and what to run.
	Manual bool
	Err    error
}

// setupTarget is where the wiring is written. It is injected rather than
// resolved inside runSetup so tests can never reach the developer's real
// Claude configuration — an earlier version resolved $HOME internally and
// the test suite duly repointed the maintainer's live MCP entry at a
// go-build test binary.
type setupTarget struct {
	// Home is the directory MCP hosts are discovered under.
	Home string
	// SettingsPath is the Claude Code settings.json hooks are merged into.
	SettingsPath string
	// Exe is the absolute binary path registered with hosts and hooks.
	Exe string
}

// realSetupTarget resolves the live machine's paths.
func realSetupTarget() (setupTarget, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return setupTarget{}, fmt.Errorf("resolve home directory: %w", err)
	}
	return setupTarget{
		Home:         home,
		SettingsPath: resolveSettingsPath(""),
		Exe:          selfExe(),
	}, nil
}

// runSetup wires tokenops into the machine it was just configured on:
// registers the MCP server with every installed host, installs the Claude
// Code hooks, and binds a subscription plan when detection is unambiguous.
//
// Two things are deliberately NOT automated. Binding a plan when detection
// cannot tell Max 20x from Pro would silently produce wrong headroom maths,
// and pointing a client's base URL at the local proxy reroutes the
// operator's real traffic — that is their call to make, not a side effect
// of running init.
func runSetup(out io.Writer, cfgPath string, target setupTarget) {
	steps := wireMCPHosts(target.Home, target.Exe)
	steps = append(steps, wireHooks(target.SettingsPath, target.Exe))
	steps = append(steps, bindPlan(cfgPath))
	renderSetup(out, steps)
}

// wireMCPHosts registers tokenops with every MCP host present on the
// machine, pinning the absolute binary path.
func wireMCPHosts(home, exe string) []setupStep {
	hosts := wire.DiscoverHosts(home)
	if len(hosts) == 0 {
		return []setupStep{{
			Name:   "MCP registration",
			Detail: "no MCP host config found — install Claude Code or Claude Desktop, then re-run `tokenops init`",
			Manual: true,
		}}
	}
	steps := make([]setupStep, 0, len(hosts))
	for _, h := range hosts {
		res, err := wire.EnsureMCPServer(h.ConfigPath, exe)
		switch {
		case err != nil:
			steps = append(steps, setupStep{Name: "MCP: " + h.Name, Err: err})
		case res.Created:
			steps = append(steps, setupStep{
				Name: "MCP: " + h.Name, Changed: true,
				Detail: "registered — restart " + h.Name + " to load the tools",
			})
		case res.Changed:
			detail := "repointed to this binary"
			if res.PreviousCommand != "" {
				detail = fmt.Sprintf("repointed from %q to this binary — restart %s to pick it up",
					res.PreviousCommand, h.Name)
			}
			steps = append(steps, setupStep{Name: "MCP: " + h.Name, Changed: true, Detail: detail})
		default:
			steps = append(steps, setupStep{Name: "MCP: " + h.Name, Detail: "already correct"})
		}
	}
	return steps
}

// wireHooks installs the Stop coaching nudge and the read dedup guard.
func wireHooks(path, exe string) setupStep {
	settings, _, err := loadSettings(path)
	if err != nil {
		return setupStep{Name: "Claude Code hooks", Err: err}
	}
	hooks := hooksMap(settings)

	var changed []string
	for _, sp := range specsFor(true, true, coachhook.DefaultBudgetUSD) {
		if ok, _ := mergeHook(hooks, sp.event, sp.matcher, commandEntry(exe, sp.args), sp.marker); ok {
			changed = append(changed, sp.name)
		}
	}
	if len(changed) == 0 {
		return setupStep{Name: "Claude Code hooks", Detail: "already wired"}
	}
	settings["hooks"] = hooks
	if err := writeSettings(path, settings); err != nil {
		return setupStep{Name: "Claude Code hooks", Err: err}
	}
	return setupStep{
		Name: "Claude Code hooks", Changed: true,
		Detail: "installed " + strings.Join(changed, " + "),
	}
}

// bindPlan sets the subscription plan when detection is unambiguous.
//
// Detection can tell that Claude Code is installed but not which tier the
// operator pays for, and the tiers differ by 4x in rate-limit headroom.
// Guessing would make every headroom figure wrong in a way that looks
// authoritative, so an ambiguous result is reported rather than resolved.
func bindPlan(cfgPath string) setupStep {
	cfg, err := config.ReadMutable(cfgPath)
	if err != nil {
		return setupStep{Name: "plan binding", Err: err}
	}
	if len(cfg.Plans) > 0 {
		return setupStep{
			Name:   "plan binding",
			Detail: fmt.Sprintf("already bound (%s)", describePlans(cfg.Plans)),
		}
	}

	found := detect.Detect(nil)
	if len(found) == 0 {
		return setupStep{
			Name:   "plan binding",
			Detail: "no AI client detected — run `tokenops plan set <provider> <plan>` once you have one",
			Manual: true,
		}
	}
	providers := map[string]bool{}
	for _, d := range found {
		providers[d.Provider] = true
	}
	names := make([]string, 0, len(providers))
	for p := range providers {
		names = append(names, p)
	}
	return setupStep{
		Name: "plan binding",
		Detail: fmt.Sprintf(
			"detected %s but not which tier you pay for — run `tokenops plan set %s claude-max-20x` (or claude-max-5x / claude-pro); headroom maths needs the right one",
			strings.Join(names, ", "), names[0]),
		Manual: true,
	}
}

func describePlans(plans map[string]string) string {
	parts := make([]string, 0, len(plans))
	for provider, plan := range plans {
		parts = append(parts, provider+"="+plan)
	}
	return strings.Join(parts, ", ")
}

// renderSetup prints what init did, separating completed work from the
// decisions still owed by the operator. The manual list is the important
// half: a setup command that reports only its successes is how a tool ends
// up half-wired without anyone noticing.
func renderSetup(out io.Writer, steps []setupStep) {
	fmt.Fprintln(out, "\nWiring tokenops into this machine:")
	var manual, failed []setupStep
	for _, s := range steps {
		switch {
		case s.Err != nil:
			fmt.Fprintf(out, "  ✗ %-22s %v\n", s.Name, s.Err)
			failed = append(failed, s)
		case s.Manual:
			fmt.Fprintf(out, "  · %-22s %s\n", s.Name, s.Detail)
			manual = append(manual, s)
		case s.Changed:
			fmt.Fprintf(out, "  ✓ %-22s %s\n", s.Name, s.Detail)
		default:
			fmt.Fprintf(out, "  = %-22s %s\n", s.Name, s.Detail)
		}
	}
	if len(manual) > 0 || len(failed) > 0 {
		fmt.Fprintf(out, "\n%d step(s) still need you — see the lines marked · and ✗ above.\n",
			len(manual)+len(failed))
	}
	fmt.Fprintln(out, "\nNext: `tokenops daemon install` to keep ingestion alive across reboot.")
}
