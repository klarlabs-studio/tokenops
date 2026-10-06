package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	"go.klarlabs.de/tokenops/internal/infra/opencodeplugin"
	"go.klarlabs.de/tokenops/internal/version"
)

// opencodePluginName is the generated file; see internal/infra/opencodeplugin
// for why it is generated and why one file serves every opencode version.
const opencodePluginName = opencodeplugin.FileName

// installOpencodePlugin writes the generated plugin. It replaces the
// separate opencode 2 file earlier versions wrote, which would otherwise
// load as a second plugin with the same id.
func installOpencodePlugin(out io.Writer, dir, exe string, readGuard, coach, routeGuard, dryRun bool, budget float64) error {
	if !readGuard && !coach && !routeGuard {
		return fmt.Errorf("nothing selected: pass --read-guard, --coach, --route-guard, or a combination")
	}
	path := filepath.Join(dir, opencodePluginName)
	body := opencodeplugin.Render(opencodeplugin.Plugin{
		Exe: exe, ReadGuard: readGuard, Coach: coach, RouteGuard: routeGuard, Budget: budget,
	}, version.String())
	_, legacy, err := opencodeplugin.ReadLegacy(dir)
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile(path); err == nil && string(existing) == body && !legacy { //nolint:gosec // our own plugin file
		fmt.Fprintln(out, "Already up to date — no changes.")
		return nil
	}
	for _, verb := range hookMarkers {
		if (verb == "read-guard" && readGuard) || (verb == "coach-hook" && coach) || (verb == "route-guard" && routeGuard) {
			fmt.Fprintf(out, "  + %s (opencode %s) -> %s\n", verb, opencodeplugin.Events[verb], opencodePluginName)
		}
	}
	if coach {
		fmt.Fprintln(out, "  · opencode 2 shows coaching nudges in its TUI, which a plugin cannot reach; they arrive on 1.x only")
	}
	if legacy {
		fmt.Fprintf(out, "  - %s (replaced: one file now serves opencode 1.x and 2)\n", opencodeplugin.LegacyV2FileName)
	}
	if dryRun {
		fmt.Fprintln(out, "\n--dry-run: not writing. Resulting plugin:")
		fmt.Fprintln(out, body)
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil { //nolint:gosec // a plugin the operator asked for
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := opencodeplugin.RemoveLegacy(dir); err != nil {
		return err
	}
	fmt.Fprintf(out, "Wrote %s\n", path)
	fmt.Fprintln(out, "\nRestart opencode to load it. It refuses a read only at")
	fmt.Fprintln(out, "`coaching.delivery: intervene`; below that it records and allows.")
	return nil
}

// statusOpencodePlugin reports which halves of the generated plugin are live.
func statusOpencodePlugin(out io.Writer, dir, exe string) error {
	path := filepath.Join(dir, opencodePluginName)
	fmt.Fprintf(out, "Client: %s\n  %s\n", hookClientOpencode, path)
	if _, legacy, err := opencodeplugin.ReadLegacy(dir); err != nil {
		return err
	} else if legacy {
		fmt.Fprintf(out, "  note: %s is from an older tokenops; the daemon folds it in on start, or run hooks install\n",
			opencodeplugin.LegacyV2FileName)
	}
	b, err := os.ReadFile(path) //nolint:gosec // a path the operator named
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(out, "No plugin at %s — no hooks wired.\n", path)
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	src := string(b)
	pluginExe := opencodeplugin.Exe(src)
	found := 0
	for _, marker := range hookMarkers {
		if !opencodeplugin.Has(src, marker) {
			continue
		}
		found++
		fmt.Fprintf(out, "  %s  event=%s -> %s\n", marker, opencodeplugin.Events[marker], pluginExe)
		if pluginExe != exe {
			fmt.Fprintf(out, "    note: points at a different binary than this one\n")
		}
	}
	if found == 0 {
		fmt.Fprintf(out, "No tokenops hooks wired in %s.\n", path)
	}
	return nil
}

// uninstallOpencodePlugin removes the named verbs from the generated plugin,
// rewriting what is left and deleting the file once nothing remains.
//
// Deleting outright would be simpler and wrong: the three halves are
// installed independently, so `uninstall --route-guard` must leave a
// still-wired read-guard alone rather than take the coaching and the read
// dedup with it.
func uninstallOpencodePlugin(out io.Writer, dir string, markers []string, dryRun bool) error {
	// A legacy opencode 2 file is folded in first, so what is removed is
	// removed from both and nothing it wired survives in a stray file.
	if !dryRun {
		if _, err := opencodeplugin.Refresh(dir, version.String(), coachhook.DefaultBudgetUSD); err != nil {
			return err
		}
	}
	path := filepath.Join(dir, opencodePluginName)
	b, err := os.ReadFile(path) //nolint:gosec // a path the operator named
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(out, "No plugin at %s — nothing to remove.\n", path)
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	src := string(b)
	p := opencodeplugin.Parse(src, coachhook.DefaultBudgetUSD)
	var removed []string
	for _, m := range markers {
		if !opencodeplugin.Has(src, m) {
			continue
		}
		removed = append(removed, m)
		switch m {
		case "read-guard":
			p.ReadGuard = false
		case "coach-hook":
			p.Coach = false
		case "route-guard":
			p.RouteGuard = false
		}
	}
	if len(removed) == 0 {
		fmt.Fprintln(out, "No tokenops hook entries found — nothing to remove.")
		return nil
	}
	for _, r := range removed {
		fmt.Fprintf(out, "  - %s (opencode %s)\n", r, opencodeplugin.Events[r])
	}
	if p.Empty() {
		if dryRun {
			fmt.Fprintf(out, "\n--dry-run: not writing. Would delete %s.\n", path)
			return nil
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		fmt.Fprintf(out, "Removed %s\n", path)
		return nil
	}
	if p.Exe == "" {
		return fmt.Errorf("%s: cannot find the binary path it calls; delete the file to remove it", path)
	}
	body := opencodeplugin.Render(p, version.String())
	if dryRun {
		fmt.Fprintln(out, "\n--dry-run: not writing. Resulting plugin:")
		fmt.Fprintln(out, body)
		return nil
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil { //nolint:gosec // a plugin the operator asked for
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Fprintf(out, "Wrote %s\n", path)
	fmt.Fprintln(out, "\nRestart opencode to load the change.")
	return nil
}

// refreshOpencodePlugin brings the opencode plugin an older tokenops
// generated up to this binary's, keeping what it wires. It runs when the
// daemon starts, which every upgrade does, so a fix to the plugin reaches
// the operator without anyone re-running hooks install. A plugin tokenops
// did not write is never touched, and a failure is logged, never fatal.
func refreshOpencodePlugin(w io.Writer) {
	dir, err := opencodePluginDir("")
	if err != nil {
		return
	}
	res, err := opencodeplugin.Refresh(dir, version.String(), coachhook.DefaultBudgetUSD)
	switch {
	case err != nil:
		fmt.Fprintf(w, "opencode plugin: %v\n", err)
	case res.Rewritten || res.RemovedLegacy:
		fmt.Fprintf(w, "opencode plugin: updated %s for opencode 1.x and 2\n", filepath.Join(dir, opencodePluginName))
	}
}

// opencodePluginDir is where opencode loads global plugins from.
func opencodePluginDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home for ~/.config/opencode/plugins: %w", err)
	}
	return filepath.Join(home, ".config", "opencode", "plugins"), nil
}

// opencodeClient reports whether the flag names opencode.
func opencodeClient(client string) bool {
	return strings.EqualFold(client, hookClientOpencode)
}
