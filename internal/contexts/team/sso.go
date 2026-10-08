package team

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// SSO is an organisation's OpenID Connect sign-in for the web view: an
// issuer (Google, Microsoft Entra ID, Keycloak, Dex in front of GitHub,
// ...), the client registered there, and the e-mail domains it may sign
// people in for.
//
// SSO only ever signs in an existing member: a verified e-mail address
// that matches no member's is refused. It never creates a member, so it
// never creates an owner, and it changes no role. Single-use sign-in links
// keep working beside it.
type SSO struct {
	Issuer   string
	ClientID string
	// SecretRef says where the client secret is: "env:NAME" or
	// "file:/path". The secret itself is never stored.
	SecretRef string
	// Domains are the e-mail domains accepted, lower case.
	Domains []string
	// AllowUnverifiedEmail accepts an ID token whose email_verified claim
	// is absent or false. Only for an issuer that controls its users'
	// addresses itself, e.g. a single Microsoft Entra tenant, whose tokens
	// carry no email_verified claim.
	AllowUnverifiedEmail bool
}

// SSOStateTTL is how long a sign-in may take between leaving for the
// issuer and coming back.
const SSOStateTTL = 10 * time.Minute

var (
	domainPattern  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
)

// Validate checks the configuration and normalises its domains.
func (c *SSO) Validate() error {
	if err := ValidateIssuer(c.Issuer); err != nil {
		return err
	}
	if c.ClientID == "" || len(c.ClientID) > 255 || strings.ContainsAny(c.ClientID, " \t\r\n") {
		return errors.New("client ID: 1 to 255 characters, no whitespace")
	}
	if _, _, err := ParseSecretRef(c.SecretRef); err != nil {
		return err
	}
	if len(c.Domains) == 0 {
		return errors.New("at least one allowed e-mail domain")
	}
	seen := map[string]bool{}
	var out []string
	for _, d := range c.Domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if !domainPattern.MatchString(d) || len(d) > 253 {
			return fmt.Errorf("e-mail domain %q: want a domain name such as example.com", d)
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	c.Domains = out
	return nil
}

// ValidateIssuer accepts an https URL, or http on a loopback address (a
// test issuer). The issuer must match the one its discovery document and
// ID tokens name exactly, so it is not rewritten here.
func ValidateIssuer(issuer string) error {
	u, err := url.Parse(issuer)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("issuer %q: want an absolute https URL with no query", issuer)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(u.Hostname()); (ip != nil && ip.IsLoopback()) || u.Hostname() == "localhost" {
			return nil
		}
	}
	return fmt.Errorf("issuer %q: must be https", issuer)
}

// ParseSecretRef reads "env:NAME" or "file:/absolute/path".
func ParseSecretRef(ref string) (kind, target string, err error) {
	kind, target, ok := strings.Cut(ref, ":")
	switch {
	case ok && kind == "env" && envNamePattern.MatchString(target):
		return kind, target, nil
	case ok && kind == "file" && strings.HasPrefix(target, "/") && !strings.ContainsRune(target, 0):
		return kind, target, nil
	}
	return "", "", fmt.Errorf("client secret %q: want env:NAME or file:/absolute/path; the secret itself is never stored", ref)
}

// NormalizeEmail lower-cases an address and checks its shape. It is how
// an address is stored on a member and compared with an ID token's.
func NormalizeEmail(email string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(email))
	local, domain, ok := strings.Cut(e, "@")
	if !ok || local == "" || strings.Contains(domain, "@") || !domainPattern.MatchString(domain) ||
		len(e) > 254 || strings.ContainsAny(e, " \t\r\n<>()[],;:\\\"") {
		return "", fmt.Errorf("e-mail %q: want an address such as ada@example.com", email)
	}
	return e, nil
}

// SSOClaims are what an ID token says about who signed in.
type SSOClaims struct {
	Email         string
	EmailVerified bool
}

// Refusals of an SSO sign-in, worded for the person signing in.
var (
	ErrSSONoEmail     = errors.New("your identity provider did not send an e-mail address; ask it for the email scope")
	ErrSSOUnverified  = errors.New("your identity provider has not verified this e-mail address")
	ErrSSODomain      = errors.New("this e-mail domain may not sign in to this organisation")
	ErrSSONoSuchEmail = errors.New("no member of this organisation has this e-mail address; ask an owner or admin to add it to your member")
)

// AcceptEmail decides whether claims may sign in under c, and returns the
// normalised address to look the member up by.
func (c SSO) AcceptEmail(claims SSOClaims) (string, error) {
	if claims.Email == "" {
		return "", ErrSSONoEmail
	}
	email, err := NormalizeEmail(claims.Email)
	if err != nil {
		return "", ErrSSONoEmail
	}
	if !claims.EmailVerified && !c.AllowUnverifiedEmail {
		return "", ErrSSOUnverified
	}
	_, domain, _ := strings.Cut(email, "@")
	for _, d := range c.Domains {
		if domain == d {
			return email, nil
		}
	}
	return "", ErrSSODomain
}
