package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderUnitFillsPlaceholders(t *testing.T) {
	bin := "/opt/homebrew/bin/tokenops"
	home := "/Users/ada"
	body, err := RenderUnit(UnitLaunchd, UnitSpec{Bin: bin, Home: home})
	if err != nil {
		t.Fatalf("render launchd: %v", err)
	}
	if strings.Contains(body, "__TOKENOPS_BIN__") || strings.Contains(body, "__HOME__") {
		t.Fatal("launchd unit still has placeholders")
	}
	if !strings.Contains(body, bin) || !strings.Contains(body, "start") {
		t.Fatalf("launchd unit missing binary/start:\n%s", body)
	}
	if !strings.Contains(body, home+"/Library/Logs/tokenops.log") {
		t.Fatalf("launchd unit missing log path:\n%s", body)
	}

	sys, err := RenderUnit(UnitSystemd, UnitSpec{Bin: bin, Home: home})
	if err != nil {
		t.Fatalf("render systemd: %v", err)
	}
	if strings.Contains(sys, "__TOKENOPS_BIN__") || strings.Contains(sys, "__HOME__") {
		t.Fatal("systemd unit still has placeholders")
	}
	if !strings.Contains(sys, "ExecStart="+bin+" start") {
		t.Fatalf("systemd ExecStart wrong:\n%s", sys)
	}
	if !strings.Contains(sys, "Environment=HOME="+home) {
		t.Fatalf("systemd HOME wrong:\n%s", sys)
	}
}

func TestRenderUnitRejectsEmptyPaths(t *testing.T) {
	if _, err := RenderUnit(UnitLaunchd, UnitSpec{Home: "/tmp"}); err == nil {
		t.Fatal("expected error for empty binary")
	}
	if _, err := RenderUnit(UnitSystemd, UnitSpec{Bin: "/bin/tokenops"}); err == nil {
		t.Fatal("expected error for empty home")
	}
}

// A unit that pins no config is the unit it was before a unit could pin
// one, to the byte: an installed unit only changes for an operator who
// pins something, and everyone else stays "Already up to date".
func TestRenderUnitWithNothingPinnedAddsNothing(t *testing.T) {
	spec := UnitSpec{Bin: "/usr/bin/tokenops", Home: "/home/ada"}
	for kind, tmpl := range map[UnitKind]string{UnitLaunchd: launchdTemplate, UnitSystemd: systemdTemplate} {
		got, err := RenderUnit(kind, spec)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		// The template with the two config placeholders taken out is the
		// template as it was.
		was := strings.NewReplacer(
			"__TOKENOPS_CONFIG_ARGS__", "", "__TOKENOPS_CONFIG_ENV__", "",
			"__TOKENOPS_BIN__", spec.Bin, "__HOME__", spec.Home,
		).Replace(tmpl)
		if got != was {
			t.Errorf("%s unit changed with nothing pinned:\n%s", kind, got)
		}
		if strings.Contains(got, "--config") || strings.Contains(got, "XDG_CONFIG_HOME") {
			t.Errorf("%s unit pins a config nobody gave:\n%s", kind, got)
		}
	}
	launchd, _ := RenderUnit(UnitLaunchd, spec)
	for _, want := range []string{"    <string>start</string>\n  </array>", "    <string>/home/ada</string>\n    <key>PATH</key>"} {
		if !strings.Contains(launchd, want) {
			t.Errorf("launchd unit lost %q:\n%s", want, launchd)
		}
	}
	systemd, _ := RenderUnit(UnitSystemd, spec)
	for _, want := range []string{"ExecStart=/usr/bin/tokenops start\n", "Environment=HOME=/home/ada\n"} {
		if !strings.Contains(systemd, want) {
			t.Errorf("systemd unit lost %q:\n%s", want, systemd)
		}
	}
}

func TestRenderUnitPinsTheConfig(t *testing.T) {
	spec := UnitSpec{Bin: "/usr/bin/tokenops", Home: "/home/ada", Config: "/etc/tokenops/a&b.yaml", ConfigHome: "/data/50%"}
	launchd, err := RenderUnit(UnitLaunchd, spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"    <string>start</string>\n    <string>--config</string>\n    <string>/etc/tokenops/a&amp;b.yaml</string>\n  </array>",
		"    <key>XDG_CONFIG_HOME</key>\n    <string>/data/50%</string>\n    <key>PATH</key>",
	} {
		if !strings.Contains(launchd, want) {
			t.Errorf("launchd unit missing %q:\n%s", want, launchd)
		}
	}
	systemd, err := RenderUnit(UnitSystemd, spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ExecStart=/usr/bin/tokenops start --config \"/etc/tokenops/a&b.yaml\"\n",
		"Environment=HOME=/home/ada\nEnvironment=\"XDG_CONFIG_HOME=/data/50%%\"\n",
	} {
		if !strings.Contains(systemd, want) {
			t.Errorf("systemd unit missing %q:\n%s", want, systemd)
		}
	}
}

// A path a unit cannot carry is refused, not written into one: a relative
// path means another file to a daemon started elsewhere, and a line break
// would start a new directive.
func TestRenderUnitRejectsAConfigItCannotCarry(t *testing.T) {
	for _, bad := range []string{"config.yaml", "./cfg/config.yaml", "/etc/a\nExecStartPre=/bin/x", "/etc/a\rb", "/etc/a\x00b", "/etc/__TOKENOPS_BIN__"} {
		for _, kind := range []UnitKind{UnitLaunchd, UnitSystemd} {
			if _, err := RenderUnit(kind, UnitSpec{Bin: "/usr/bin/tokenops", Home: "/home/ada", Config: bad}); err == nil {
				t.Errorf("%s: config %q was rendered", kind, bad)
			}
			if _, err := RenderUnit(kind, UnitSpec{Bin: "/usr/bin/tokenops", Home: "/home/ada", ConfigHome: bad}); err == nil {
				t.Errorf("%s: config directory %q was rendered", kind, bad)
			}
		}
	}
}

func TestWriteUnitIs0600AndIdempotentCompare(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokenops.service")
	body, err := RenderUnit(UnitSystemd, UnitSpec{Bin: "/usr/bin/tokenops", Home: "/home/ada"})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteUnit(path, body); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("unit mode = %o; want 0600", st.Mode().Perm())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !UnitEqual(string(got), body) {
		t.Fatal("written unit does not match rendered body")
	}
}

func TestDefaultUnitPath(t *testing.T) {
	home := "/home/ada"
	if got := DefaultUnitPath(UnitSystemd, home); got != "/home/ada/.config/systemd/user/tokenops.service" {
		t.Errorf("systemd path = %s", got)
	}
	if got := DefaultUnitPath(UnitLaunchd, home); got != "/home/ada/Library/LaunchAgents/de.klarlabs.tokenops.plist" {
		t.Errorf("launchd path = %s", got)
	}
}

func TestDeployTemplatesMatchEmbed(t *testing.T) {
	pairs := []struct {
		embed, deploy string
	}{
		{"embed/launchd.plist", filepath.Join("..", "..", "deploy", "launchd", "de.klarlabs.tokenops.plist")},
		{"embed/systemd.service", filepath.Join("..", "..", "deploy", "systemd", "tokenops.service")},
	}
	for _, p := range pairs {
		embedB, err := os.ReadFile(p.embed)
		if err != nil {
			t.Fatalf("read embed %s: %v", p.embed, err)
		}
		deployB, err := os.ReadFile(p.deploy)
		if err != nil {
			t.Fatalf("read deploy %s: %v", p.deploy, err)
		}
		if !UnitEqual(string(embedB), string(deployB)) {
			t.Errorf("%s drifted from %s — copy the embed template to deploy/", p.deploy, p.embed)
		}
	}
}

func TestParseUnitKind(t *testing.T) {
	if _, err := ParseUnitKind("nope"); err == nil {
		t.Fatal("expected error for unknown kind")
	}
	k, err := ParseUnitKind("launchd")
	if err != nil || k != UnitLaunchd {
		t.Fatalf("launchd: %v %q", err, k)
	}
}
