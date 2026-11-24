package cli

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The action the surfaces print for Cursor works as printed: the cookie
// alone, from the environment, the user ID read from its start.
func TestEnableCursorFromTheCookieAlone(t *testing.T) {
	tests := []struct {
		name, cookie, wantCookie, wantUser string
	}{
		{"encoded, as the browser shows it", "user_01ABC%3A%3Aeyj.x.y", "user_01ABC%3A%3Aeyj.x.y", "user_01ABC"},
		{"pasted with its name", "WorkosCursorSessionToken=user_01ABC::tok; other=1", "user_01ABC::tok", "user_01ABC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			writeSeedConfig(t, path)
			t.Setenv("TOKENOPS_CURSOR_COOKIE", tt.cookie)
			cfg, out, err := runEnable(t, path, "cursor")
			if err != nil {
				t.Fatalf("enable: %v\n%s", err, out)
			}
			c := cfg.VendorUsage.Cursor
			if !c.Enabled || c.Cookie != tt.wantCookie || c.UserID != tt.wantUser {
				t.Errorf("cursor = enabled %v, cookie %q, user %q; want %q, %q", c.Enabled, c.Cookie, c.UserID, tt.wantCookie, tt.wantUser)
			}
		})
	}
}

// Switching a plan source off dismisses its provider; on takes that back.
// A transcript reader switched off is no answer about the plan.
func TestDisableDismissesThePlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeSeedConfig(t, path)
	cfg, _, err := runEnable(t, path, "cursor", "--disable")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"cursor"}; !reflect.DeepEqual(cfg.VendorUsage.Dismissed, want) {
		t.Errorf("dismissed = %v, want %v", cfg.VendorUsage.Dismissed, want)
	}
	if cfg, _, err = runEnable(t, path, "codex-jsonl", "--disable"); err != nil || len(cfg.VendorUsage.Dismissed) != 1 {
		t.Errorf("a transcript reader off changed the dismissals: %v, %v", cfg.VendorUsage.Dismissed, err)
	}
	if cfg, _, err = runEnable(t, path, "cursor", "--cookie", "user_1::t"); err != nil || cfg.VendorUsage.Dismissed != nil {
		t.Errorf("connecting cursor left it dismissed: %v, %v", cfg.VendorUsage.Dismissed, err)
	}
}

func TestVendorUsageDismiss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeSeedConfig(t, path)
	run := func(args ...string) (string, error) {
		cmd := withConfigFlag(newVendorUsageDismissCmd())
		cmd.SetArgs(append(args, "--config", path))
		out := &bytes.Buffer{}
		cmd.SetOut(out)
		cmd.SetErr(out)
		return out.String(), cmd.Execute()
	}
	if _, err := run("cursor"); err != nil {
		t.Fatal(err)
	}
	cfg, err := readMutableConfig(path)
	if err != nil || !reflect.DeepEqual(cfg.VendorUsage.Dismissed, []string{"cursor"}) {
		t.Fatalf("dismissed = %v, %v", cfg.VendorUsage.Dismissed, err)
	}
	if _, err := run("cursor", "--undo"); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = readMutableConfig(path); cfg.VendorUsage.Dismissed != nil {
		t.Errorf("after --undo: %v", cfg.VendorUsage.Dismissed)
	}
	if _, err := run("no-such-tool"); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("unknown provider: %v", err)
	}
}

func TestCursorUserFromCookie(t *testing.T) {
	for cookie, want := range map[string]string{
		"user_01ABC%3A%3Aeyj": "user_01ABC",
		"user_01ABC::eyj":     "user_01ABC",
		"eyj.no.user":         "",
		"user_x":              "",
		"":                    "",
	} {
		if got := cursorUserFromCookie(cookie); got != want {
			t.Errorf("cursorUserFromCookie(%q) = %q, want %q", cookie, got, want)
		}
	}
}
