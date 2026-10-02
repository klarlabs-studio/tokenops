package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func stubOpencodeVersions(t *testing.T, v1, v2 bool) {
	t.Helper()
	prev := opencodeVersions
	opencodeVersions = func() (bool, bool) { return v1, v2 }
	t.Cleanup(func() { opencodeVersions = prev })
}

// With opencode 2 installed the 2.x plugin is written; with both, both,
// side by side (each version skips the other's file).
func TestInstallOpencodeWritesThePluginForEachVersion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		v1, v2 bool
	}{{"v2 only", false, true}, {"both", true, true}, {"v1 only", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			stubOpencodeVersions(t, tc.v1, tc.v2)
			dir := t.TempDir()
			var out bytes.Buffer
			if err := installOpencodePlugin(&out, dir, "/bin/tokenops", true, true, true, false, 50); err != nil {
				t.Fatal(err)
			}
			_, errV1 := os.Stat(filepath.Join(dir, opencodePluginName))
			_, errV2 := os.Stat(filepath.Join(dir, opencodeV2PluginName))
			if (errV1 == nil) != tc.v1 || (errV2 == nil) != tc.v2 {
				t.Errorf("v1 file %v, v2 file %v; want %v, %v\n%s", errV1 == nil, errV2 == nil, tc.v1, tc.v2, out.String())
			}
		})
	}
}

func TestOpencodeV2PluginShape(t *testing.T) {
	body := renderOpencodeV2("/bin/tokenops", true, true)
	for _, want := range []string{
		`export default {`, `id: "tokenops"`, `ctx.tool.hook("execute.before"`,
		`args.path ?? args.filePath`, `throw new Error(denyReason)`,
		`ctx.session.hook("prompt"`, `event.prompt.text = text`,
		`run(["read-guard"]`, `run(["route-guard"]`, `const TOKENOPS = "/bin/tokenops"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("plugin lacks %q", want)
		}
	}
	if strings.Contains(body, "coach-hook") {
		t.Error("a server plugin cannot show a nudge in opencode 2's TUI")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; syntax not checked")
	}
	path := filepath.Join(t.TempDir(), "plugin.mjs")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil { //nolint:gosec // test-only
		t.Fatalf("generated plugin is not valid JavaScript: %v\n%s", err, out)
	}
}

func TestUninstallOpencodeV2KeepsTheOtherHalf(t *testing.T) {
	stubOpencodeVersions(t, false, true)
	dir := t.TempDir()
	var out bytes.Buffer
	if err := installOpencodePlugin(&out, dir, "/bin/tokenops", true, false, true, false, 50); err != nil {
		t.Fatal(err)
	}
	if err := uninstallOpencodePlugin(&out, dir, []string{"route-guard"}, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, opencodeV2PluginName))
	if err != nil {
		t.Fatal(err)
	}
	if !opencodeMarkerPresent(string(b), "read-guard") || opencodeMarkerPresent(string(b), "route-guard") {
		t.Errorf("after removing route-guard:\n%s", b)
	}
	if err := uninstallOpencodePlugin(&out, dir, []string{"read-guard"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, opencodeV2PluginName)); !os.IsNotExist(err) {
		t.Error("an empty plugin was left behind")
	}
}

func TestStatusShowsTheOpencodeV2Plugin(t *testing.T) {
	stubOpencodeVersions(t, true, true)
	dir := t.TempDir()
	var out bytes.Buffer
	if err := installOpencodePlugin(&out, dir, "/bin/tokenops", true, false, false, false, 50); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := statusOpencodePlugin(&out, dir, "/bin/tokenops"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "(opencode 2)") || !strings.Contains(out.String(), "read-guard  event=tool.execute.before") {
		t.Errorf("status:\n%s", out.String())
	}
}
