package fireworks

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The shape of `nexus status --json` is not documented field by field,
// so the parser looks for values of the right shape under fitting names.
// These fixtures are plausible shapes, not captured output.
func TestParseNexusStatus(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want SignIn
	}{
		{"resource name", `{"signedIn":true,"user":"accounts/acme/users/u-ada","email":"ada@example.com"}`,
			SignIn{Account: "acme", User: "u-ada", Email: "ada@example.com"}},
		{"nested ids", `{"auth":{"signedIn":true,"account":{"id":"acme"},"user":{"id":"u-ada","email":"ada@example.com"}},"keyStorage":{"backend":"keychain"}}`,
			SignIn{Account: "acme", User: "u-ada", Email: "ada@example.com"}},
		{"flat ids", `{"accountId":"acme","userId":"u-ada"}`, SignIn{Account: "acme", User: "u-ada"}},
		{"account resource", `{"account":"accounts/acme","signedInAs":"ada@example.com"}`, SignIn{Account: "acme", Email: "ada@example.com"}},
		{"email only", `{"signed_in":true,"email":"ada@example.com","key_storage":"keychain"}`, SignIn{Email: "ada@example.com"}},
		{"key preview ignored", `{"apiKey":"fw_abc123","key":{"id":"fw_x"},"email":"ada@example.com"}`, SignIn{Email: "ada@example.com"}},
		{"signed out", `{"signedIn":false}`, SignIn{}},
		{"not json", `Signed in as ada@example.com`, SignIn{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseNexusStatus([]byte(c.out))
			if got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
			for _, v := range []string{got.Account, got.User, got.Email} {
				if strings.Contains(v, "fw_") {
					t.Errorf("returned a key: %q", v)
				}
			}
		})
	}
}

// Nexus CLI's status comes before the file scan: a user it names is used
// as is, an email it names is looked up.
func TestPollerAsksNexusStatusFirst(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".nexus/config.json"), `{"email":"file@example.com"}`)
	api := &fakeAPI{stamp: "k1", users: map[string]string{"ada@example.com": "u-ada", "file@example.com": "u-file"}}
	status := func(context.Context) SignIn { return SignIn{Account: "acme", User: "u-status"} }
	newPollerFor(api, home, PollerOptions{SignIn: status}).Scan(context.Background())
	if api.readUser != "u-status" || api.finds != 0 {
		t.Errorf("status user: read %q after %d lookups", api.readUser, api.finds)
	}
	api = &fakeAPI{stamp: "k1", users: map[string]string{"ada@example.com": "u-ada", "file@example.com": "u-file"}}
	status = func(context.Context) SignIn { return SignIn{Email: "ada@example.com"} }
	newPollerFor(api, home, PollerOptions{SignIn: status}).Scan(context.Background())
	if api.readUser != "u-ada" {
		t.Errorf("status email: read %q", api.readUser)
	}
	api = &fakeAPI{stamp: "k1", users: map[string]string{"file@example.com": "u-file"}}
	status = func(context.Context) SignIn { return SignIn{} }
	newPollerFor(api, home, PollerOptions{SignIn: status}).Scan(context.Background())
	if api.readUser != "u-file" {
		t.Errorf("file fallback: read %q", api.readUser)
	}
}

// A user set by hand that the account does not have is said, with the
// operator's own values, instead of passing for success.
func TestPollerNamesAHandSetUserThatFailed(t *testing.T) {
	api := &fakeAPI{stamp: "k1", readResult: Reading{Scope: ScopeAccount, Fallback: "user u-42 not found in account acme"}}
	r, _ := newPollerFor(api, t.TempDir(), PollerOptions{UserID: "u-42"}).read(context.Background())
	if !strings.HasPrefix(r.Fallback, "user u-42 not found in account acme") || !strings.Contains(r.Fallback, "--user-id") {
		t.Errorf("fallback %q", r.Fallback)
	}
}
