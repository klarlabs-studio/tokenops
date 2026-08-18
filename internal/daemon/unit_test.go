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
	body, err := RenderUnit(UnitLaunchd, bin, home)
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

	sys, err := RenderUnit(UnitSystemd, bin, home)
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
	if _, err := RenderUnit(UnitLaunchd, "", "/tmp"); err == nil {
		t.Fatal("expected error for empty binary")
	}
	if _, err := RenderUnit(UnitSystemd, "/bin/tokenops", ""); err == nil {
		t.Fatal("expected error for empty home")
	}
}

func TestWriteUnitIs0600AndIdempotentCompare(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokenops.service")
	body, err := RenderUnit(UnitSystemd, "/usr/bin/tokenops", "/home/ada")
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
