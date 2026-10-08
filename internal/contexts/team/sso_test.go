package team

import (
	"errors"
	"testing"
)

func TestSSOValidate(t *testing.T) {
	good := SSO{Issuer: "https://accounts.google.com", ClientID: "abc.apps.googleusercontent.com",
		SecretRef: "env:TEAM_SSO_SECRET", Domains: []string{" Example.COM ", "example.com", "example.de"}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(good.Domains) != 2 || good.Domains[0] != "example.com" || good.Domains[1] != "example.de" {
		t.Errorf("domains not normalised: %v", good.Domains)
	}
	bad := map[string]func(*SSO){
		"http issuer":         func(c *SSO) { c.Issuer = "http://idp.example.com" },
		"issuer with query":   func(c *SSO) { c.Issuer = "https://idp.example.com/?x=1" },
		"issuer with user":    func(c *SSO) { c.Issuer = "https://u:p@idp.example.com" },
		"no client":           func(c *SSO) { c.ClientID = "" },
		"client whitespace":   func(c *SSO) { c.ClientID = "a b" },
		"inline secret":       func(c *SSO) { c.SecretRef = "s3cret" },
		"relative file":       func(c *SSO) { c.SecretRef = "file:secret.txt" },
		"bad env name":        func(c *SSO) { c.SecretRef = "env:1X" },
		"no domains":          func(c *SSO) { c.Domains = nil },
		"wildcard domain":     func(c *SSO) { c.Domains = []string{"*.example.com"} },
		"address as domain":   func(c *SSO) { c.Domains = []string{"ada@example.com"} },
		"single-label domain": func(c *SSO) { c.Domains = []string{"localhost"} },
	}
	for name, mutate := range bad {
		c := SSO{Issuer: "https://idp.example.com", ClientID: "x", SecretRef: "env:X", Domains: []string{"example.com"}}
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, ok := range []string{"http://127.0.0.1:5556/dex", "http://localhost:8080", "https://login.microsoftonline.com/0000/v2.0"} {
		if err := ValidateIssuer(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
}

func TestSSOAcceptEmail(t *testing.T) {
	c := SSO{Domains: []string{"example.com"}}
	cases := []struct {
		name   string
		claims SSOClaims
		allow  bool
		want   string
		err    error
	}{
		{"verified", SSOClaims{Email: "Ada@Example.com", EmailVerified: true}, false, "ada@example.com", nil},
		{"unverified", SSOClaims{Email: "ada@example.com"}, false, "", ErrSSOUnverified},
		{"unverified allowed", SSOClaims{Email: "ada@example.com"}, true, "ada@example.com", nil},
		{"other domain", SSOClaims{Email: "ada@evil.example", EmailVerified: true}, false, "", ErrSSODomain},
		{"subdomain is another domain", SSOClaims{Email: "ada@eng.example.com", EmailVerified: true}, false, "", ErrSSODomain},
		{"suffix trick", SSOClaims{Email: "ada@notexample.com", EmailVerified: true}, false, "", ErrSSODomain},
		{"no email", SSOClaims{EmailVerified: true}, false, "", ErrSSONoEmail},
		{"two at signs", SSOClaims{Email: "ada@evil.example@example.com", EmailVerified: true}, false, "", ErrSSONoEmail},
	}
	for _, tc := range cases {
		c.AllowUnverifiedEmail = tc.allow
		got, err := c.AcceptEmail(tc.claims)
		if got != tc.want || !errors.Is(err, tc.err) {
			t.Errorf("%s: got %q, %v; want %q, %v", tc.name, got, err, tc.want, tc.err)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	if e, err := NormalizeEmail("  Ada.Lovelace@Example.EU "); err != nil || e != "ada.lovelace@example.eu" {
		t.Errorf("got %q, %v", e, err)
	}
	for _, bad := range []string{"", "ada", "@example.com", "ada@", "ada@example", "a da@example.com", "<ada@example.com>"} {
		if _, err := NormalizeEmail(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParseSecretRef(t *testing.T) {
	if k, v, err := ParseSecretRef("file:/run/secrets/oidc"); err != nil || k != "file" || v != "/run/secrets/oidc" {
		t.Errorf("file: %s %s %v", k, v, err)
	}
	if k, v, err := ParseSecretRef("env:TEAM_OIDC_SECRET"); err != nil || k != "env" || v != "TEAM_OIDC_SECRET" {
		t.Errorf("env: %s %s %v", k, v, err)
	}
}
