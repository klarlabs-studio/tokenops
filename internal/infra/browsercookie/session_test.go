package browsercookie

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// chromeHome makes a fake home with one Chrome profile whose cookie store
// holds cookies (host, name, value), encrypted under secret.
func chromeHome(t *testing.T, secret string, cookies [][3]string) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	profile := filepath.Join(home, "Library/Application Support/Google/Chrome/Default")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	first := cookies[0]
	store := chromiumFixture(t, secret, first[0], first[1], first[2], false)
	if err := os.Rename(store, filepath.Join(profile, "Cookies")); err != nil {
		t.Fatal(err)
	}
	for _, c := range cookies[1:] {
		addChromiumCookie(t, filepath.Join(profile, "Cookies"), secret, c[0], c[1], c[2])
	}
	return home
}

// countingSecret answers with secret and counts how often it was asked.
func countingSecret(secret string, asked *int) SecretFunc {
	return func(Browser) (string, error) {
		*asked++
		return secret, nil
	}
}

// A console session lives on the parent domain and on the console's own
// host; the browser sends both to the console, so both are read, in the
// order named, prefixes included, and macOS is asked once, not per cookie.
func TestFindSessionReadsWhatTheBrowserSendsWithOnePrompt(t *testing.T) {
	const secret = "keychain-secret"
	home := chromeHome(t, secret, [][3]string{
		{".alibabacloud.com", "login_aliyunid_ticket", "ticket"},
		{"modelstudio.console.alibabacloud.com", "sec_token", "tok"},
		{".alibabacloud.com", "ory_session_abc", "ory"},
		{"other.example.com", "login_aliyunid_pk", "not-this-site"},
		{".console.alibabacloud.com", "login_aliyunid_pk", "pk"},
	})
	asked := 0
	got, err := FindSession(context.Background(), home, Session{
		Hosts: []string{"modelstudio.console.alibabacloud.com"},
		Names: []string{"login_aliyunid_ticket", "login_aliyunid_pk", "sec_token", "ory_session_*", "absent"},
	}, "", countingSecret(secret, &asked))
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if want := "login_aliyunid_ticket=ticket; login_aliyunid_pk=pk; sec_token=tok; ory_session_abc=ory"; got.Header() != want {
		t.Errorf("header has %d cookies, want the four named ones in order", len(got.Cookies))
	}
	if asked != 1 {
		t.Errorf("the Keychain was asked %d times, want once", asked)
	}
	if got.Browser.Name != "Chrome" || got.Host != "modelstudio.console.alibabacloud.com" {
		t.Errorf("found in %q for %q", got.Browser.Name, got.Host)
	}
}

// A store with no proof cookie has no session: nothing in it is
// decrypted, so macOS never asks for a browser that is not signed in.
func TestFindSessionWithoutProofNeverAsks(t *testing.T) {
	home := chromeHome(t, "s", [][3]string{{".qoder.com", "_ga", "analytics"}})
	asked := 0
	_, err := FindSession(context.Background(), home, Session{
		Hosts: []string{"qoder.com"}, Names: []string{"session", "_ga"},
	}, "", countingSecret("s", &asked))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if asked != 0 {
		t.Errorf("the Keychain was asked %d times for a store with no session", asked)
	}
}

// The second host is tried when the first has no session, and the
// cookies all come from the host that has it.
func TestFindSessionTriesHostsInOrder(t *testing.T) {
	home := chromeHome(t, "s", [][3]string{
		{"qoder.com.cn", "sid", "cn-session"},
		{".qoder.com.cn", "csrf", "cn-csrf"},
	})
	asked := 0
	got, err := FindSession(context.Background(), home, Session{
		Hosts: []string{"qoder.com", "qoder.com.cn"}, AllForHost: true,
	}, "", countingSecret("s", &asked))
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Host != "qoder.com.cn" || got.Header() != "csrf=cn-csrf; sid=cn-session" {
		t.Errorf("found %d cookies for %q", len(got.Cookies), got.Host)
	}
}

// Proof names a session cookie among several: a store holding only the
// CSRF cookie is not a session.
func TestFindSessionProof(t *testing.T) {
	home := chromeHome(t, "s", [][3]string{{"admin.mistral.ai", "csrftoken", "c"}})
	s := Session{Hosts: []string{"admin.mistral.ai"}, Names: []string{"csrftoken", "ory_session_*"}, Proof: []string{"ory_session_*"}}
	if _, err := FindSession(context.Background(), home, s, "", countingSecret("s", new(int))); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	addChromiumCookie(t, filepath.Join(home, "Library/Application Support/Google/Chrome/Default/Cookies"), "s", ".mistral.ai", "ory_session_x", "o")
	got, err := FindSession(context.Background(), home, s, "", countingSecret("s", new(int)))
	if err != nil || got.Header() != "csrftoken=c; ory_session_x=o" {
		t.Errorf("found %d cookies, err %v", len(got.Cookies), err)
	}
}

func TestHostVariantsIncludeParentDomains(t *testing.T) {
	got := hostVariants("Bailian.Console.Aliyun.com")
	want := []string{"bailian.console.aliyun.com", ".bailian.console.aliyun.com", ".console.aliyun.com", ".aliyun.com"}
	if len(got) != len(want) {
		t.Fatalf("variants %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("variants %v, want %v", got, want)
		}
	}
	if v := hostVariants("claude.ai"); len(v) != 2 {
		t.Errorf("a registrable domain has no parent to add: %v", v)
	}
}
