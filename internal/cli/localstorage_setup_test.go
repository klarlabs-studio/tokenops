package cli

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/providersetup"
	"go.klarlabs.de/tokenops/internal/config"
)

// fakeLocalStorage replaces the browser read: no real profile is touched.
func fakeLocalStorage(t *testing.T, session string, err error) *int {
	t.Helper()
	calls := 0
	prev := localStorageSession
	localStorageSession = func(p providersetup.Provider, _ string) (string, string, error) {
		calls++
		if p.LocalStorage == nil {
			t.Errorf("%s has no localStorage", p.ID)
		}
		return session, "Chrome", err
	}
	t.Cleanup(func() { localStorageSession = prev })
	return &calls
}

const windsurfSession = `{"devin_account_id":"a","devin_auth1_token":"t1","devin_primary_org_id":"o","devin_session_token":"s"}`

// Setup reads Windsurf's session from localStorage, says exactly what,
// checks it once, and stores it like a pasted one: the daemon never reads
// the browser for it.
func TestWindsurfSetupReadsLocalStorage(t *testing.T) {
	calls := fakeLocalStorage(t, windsurfSession, nil)
	sent := fakeVerify(t, "windsurf", windsurfSession)
	path := seedConfig(t)
	out, err := runCookieSetupCmd(t, "", "windsurf", "--config", path, "--no-restart")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"localStorage", "devin_session_token", "no Keychain item", "never reads your browser", "Found it in Chrome"} {
		if !strings.Contains(out, want) {
			t.Errorf("output omits %q:\n%s", want, out)
		}
	}
	if *calls != 1 || len(*sent) != 1 || strings.Contains(out, `"s"}`) {
		t.Errorf("calls %d, verified %v:\n%s", *calls, *sent, out)
	}
	cfg, _ := config.ReadMutable(path)
	if c := cfg.VendorUsage.Accounts.Credentials["windsurf"]; c.Key != windsurfSession || c.FromBrowser || c.Browser != "" {
		t.Errorf("stored %+v", c)
	}
}

// With no session in a browser, the paste prompt is the fallback; --paste
// never reads the browser.
func TestWindsurfSetupFallsBackToPaste(t *testing.T) {
	calls := fakeLocalStorage(t, "", providersetup.ErrNoLocalStorage)
	fakeVerify(t, "windsurf", windsurfSession)
	path := seedConfig(t)
	out, err := runCookieSetupCmd(t, windsurfSession+"\n", "windsurf", "--config", path, "--no-restart")
	if err != nil || *calls != 1 || !strings.Contains(out, "paste it below") {
		t.Fatalf("%v (%d calls)\n%s", err, *calls, out)
	}
	*calls = 0
	path = seedConfig(t)
	if out, err := runCookieSetupCmd(t, windsurfSession+"\n", "windsurf", "--paste", "--config", path, "--no-restart"); err != nil || *calls != 0 {
		t.Errorf("--paste read the browser (%d) or failed: %v\n%s", *calls, err, out)
	}
}
