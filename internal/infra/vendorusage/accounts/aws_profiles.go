package accounts

import (
	"bufio"
	"context"
	"crypto/sha1" //nolint:gosec // the AWS CLI names its SSO token cache by SHA-1; not a security use
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// awsProfiles resolves a named AWS profile to signing credentials the way
// the AWS SDKs do, without the AWS CLI and without ever prompting:
//
//   - an assume-role profile (role_arn with source_profile, or
//     credential_source = Environment) asks STS AssumeRole, signed with the
//     source's credentials;
//   - an SSO profile reads the token `aws sso login` cached in
//     ~/.aws/sso/cache, read-only, and asks the SSO portal for the role's
//     credentials; an expired token is refused with the hint to sign in
//     again, never refreshed;
//   - otherwise the profile's static keys.
//
// Not read: a profile that needs an MFA code (mfa_serial), which only a
// prompt can give, and credential_process, which runs a program the
// operator configured and TokenOps cannot know will not prompt (aws-vault,
// the 1Password CLI and browser-based helpers do), from a background
// daemon.
type awsProfiles struct {
	home    string
	getenv  func(string) string
	hc      *http.Client
	ssoBase string // overrides https://portal.sso.<region>.amazonaws.com
	stsBase string // overrides https://sts.amazonaws.com
	now     func() time.Time
}

// errAWSUnsupported is a profile that would need a prompt or a program.
var errAWSUnsupported = errors.New("accounts: this AWS profile needs a prompt or a program to sign in, which TokenOps never runs")

// maxSourceDepth bounds a chain of source_profile hops.
const maxSourceDepth = 4

func (a awsProfiles) path(env, rel string) string {
	if p := strings.TrimSpace(a.getenv(env)); p != "" {
		return p
	}
	return filepath.Join(a.home, ".aws", rel)
}

// sections reads both of AWS's files: the credentials file's [name] and the
// config file's [profile name], [default] and [sso-session name], merged
// with the credentials file winning, as the SDKs merge them.
func (a awsProfiles) sections() (profiles, ssoSessions map[string]map[string]string, err error) {
	profiles, ssoSessions = map[string]map[string]string{}, map[string]map[string]string{}
	add := func(m map[string]map[string]string, name, k, v string, override bool) {
		if m[name] == nil {
			m[name] = map[string]string{}
		}
		if _, set := m[name][k]; override || !set {
			m[name][k] = v
		}
	}
	if err := readAWSINI(a.path("AWS_CONFIG_FILE", "config"), func(section, k, v string) {
		switch {
		case section == "default":
			add(profiles, "default", k, v, false)
		case strings.HasPrefix(section, "profile "):
			add(profiles, strings.TrimSpace(strings.TrimPrefix(section, "profile ")), k, v, false)
		case strings.HasPrefix(section, "sso-session "):
			add(ssoSessions, strings.TrimSpace(strings.TrimPrefix(section, "sso-session ")), k, v, false)
		}
	}); err != nil {
		return nil, nil, err
	}
	if err := readAWSINI(a.path("AWS_SHARED_CREDENTIALS_FILE", "credentials"), func(section, k, v string) {
		add(profiles, section, k, v, true)
	}); err != nil {
		return nil, nil, err
	}
	return profiles, ssoSessions, nil
}

// readAWSINI calls set for every key in an AWS INI file; a missing file has
// none.
func readAWSINI(path string, set func(section, k, v string)) error {
	f, err := os.Open(path) // #nosec G304 -- AWS's own files, chosen as the AWS SDKs choose them
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()
	section := ""
	sc := bufio.NewScanner(io.LimitReader(f, 1<<20))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || section == "" {
			continue
		}
		set(section, strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v))
	}
	return sc.Err()
}

// credentials resolves profile name, saying how.
func (a awsProfiles) credentials(ctx context.Context, name string) (awsCredentials, string, error) {
	profiles, sessions, err := a.sections()
	if err != nil {
		return awsCredentials{}, "", err
	}
	return a.resolve(ctx, profiles, sessions, name, 0)
}

func (a awsProfiles) resolve(ctx context.Context, profiles, sessions map[string]map[string]string, name string, depth int) (awsCredentials, string, error) {
	p, ok := profiles[name]
	if !ok {
		return awsCredentials{}, "", errNoAWSCredentials
	}
	switch {
	case p["role_arn"] != "":
		return a.assumeRole(ctx, profiles, sessions, name, p, depth)
	case p["sso_session"] != "" || p["sso_start_url"] != "":
		return a.sso(ctx, name, p, sessions)
	case p["aws_access_key_id"] != "" && p["aws_secret_access_key"] != "":
		return awsCredentials{AccessKeyID: p["aws_access_key_id"], SecretAccessKey: p["aws_secret_access_key"], SessionToken: p["aws_session_token"]},
			"the [" + name + "] profile's keys", nil
	case p["credential_process"] != "":
		return awsCredentials{}, "", fmt.Errorf("%w: the [%s] profile uses credential_process", errAWSUnsupported, name)
	}
	return awsCredentials{}, "", errNoAWSCredentials
}

// assumeRole asks STS for the role's credentials, signed with the source
// profile's (or the environment's) credentials.
func (a awsProfiles) assumeRole(ctx context.Context, profiles, sessions map[string]map[string]string, name string, p map[string]string, depth int) (awsCredentials, string, error) {
	if p["mfa_serial"] != "" {
		return awsCredentials{}, "", fmt.Errorf("%w: the [%s] profile needs an MFA code", errAWSUnsupported, name)
	}
	if depth >= maxSourceDepth {
		return awsCredentials{}, "", fmt.Errorf("accounts: the [%s] profile's source_profile chain is too long", name)
	}
	var (
		src    awsCredentials
		origin string
		err    error
	)
	switch {
	case p["source_profile"] == name:
		// A profile that is its own source signs with its own static keys.
		if p["aws_access_key_id"] == "" || p["aws_secret_access_key"] == "" {
			return awsCredentials{}, "", errNoAWSCredentials
		}
		src = awsCredentials{AccessKeyID: p["aws_access_key_id"], SecretAccessKey: p["aws_secret_access_key"], SessionToken: p["aws_session_token"]}
		origin = "its own keys"
	case p["source_profile"] != "":
		if src, origin, err = a.resolve(ctx, profiles, sessions, p["source_profile"], depth+1); err != nil {
			return awsCredentials{}, "", err
		}
	case strings.EqualFold(p["credential_source"], "Environment"):
		src = awsCredentials{AccessKeyID: strings.TrimSpace(a.getenv("AWS_ACCESS_KEY_ID")),
			SecretAccessKey: strings.TrimSpace(a.getenv("AWS_SECRET_ACCESS_KEY")), SessionToken: strings.TrimSpace(a.getenv("AWS_SESSION_TOKEN"))}
		if src.AccessKeyID == "" || src.SecretAccessKey == "" {
			return awsCredentials{}, "", errNoAWSCredentials
		}
		origin = "$AWS_ACCESS_KEY_ID"
	default:
		// Ec2InstanceMetadata and EcsContainer read a metadata service, not
		// this machine's sign-in.
		return awsCredentials{}, "", fmt.Errorf("%w: the [%s] profile's credential_source is not read", errAWSUnsupported, name)
	}
	form := url.Values{
		"Action":          {"AssumeRole"},
		"Version":         {"2011-06-15"},
		"RoleArn":         {p["role_arn"]},
		"RoleSessionName": {"tokenops-usage"},
		"DurationSeconds": {"900"},
	}
	if s := p["role_session_name"]; s != "" {
		form.Set("RoleSessionName", s)
	}
	if id := p["external_id"]; id != "" {
		form.Set("ExternalId", id)
	}
	c, err := a.stsAssumeRole(ctx, src, form)
	if err != nil {
		return awsCredentials{}, "", err
	}
	return c, "the [" + name + "] profile's role, assumed with " + origin, nil
}

func (a awsProfiles) stsAssumeRole(ctx context.Context, src awsCredentials, form url.Values) (awsCredentials, error) {
	body := []byte(form.Encode())
	endpoint := base(a.stsBase, "https://sts.amazonaws.com") + "/"
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return awsCredentials{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	signV4(req, body, src, "us-east-1", "sts", a.now())
	resp, err := a.client().Do(req)
	if err != nil {
		return awsCredentials{}, fmt.Errorf("accounts: POST %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest {
			return awsCredentials{}, fmt.Errorf("%w (STS AssumeRole refused: %d)", usage.ErrAuth, resp.StatusCode)
		}
		return awsCredentials{}, fmt.Errorf("accounts: POST %s: status %d", endpoint, resp.StatusCode)
	}
	var out struct {
		Credentials struct {
			AccessKeyID     string `xml:"AccessKeyId"`
			SecretAccessKey string `xml:"SecretAccessKey"`
			SessionToken    string `xml:"SessionToken"`
		} `xml:"AssumeRoleResult>Credentials"`
	}
	if err := xml.Unmarshal(raw, &out); err != nil || out.Credentials.AccessKeyID == "" || out.Credentials.SecretAccessKey == "" {
		return awsCredentials{}, fmt.Errorf("accounts: POST %s: unexpected answer", endpoint)
	}
	return awsCredentials{AccessKeyID: out.Credentials.AccessKeyID, SecretAccessKey: out.Credentials.SecretAccessKey,
		SessionToken: out.Credentials.SessionToken}, nil
}

// sso reads the token `aws sso login` cached, read-only, and asks the SSO
// portal for the profile's role credentials. It never refreshes the token:
// that is `aws sso login`'s, and an expired one is refused with the hint.
func (a awsProfiles) sso(ctx context.Context, name string, p map[string]string, sessions map[string]map[string]string) (awsCredentials, string, error) {
	account, role := p["sso_account_id"], p["sso_role_name"]
	region, cacheKey := p["sso_region"], p["sso_start_url"]
	if s := p["sso_session"]; s != "" {
		sess, ok := sessions[s]
		if !ok {
			return awsCredentials{}, "", fmt.Errorf("accounts: the [%s] profile names sso-session %q, which is not in the AWS config file", name, s)
		}
		region, cacheKey = sess["sso_region"], s
	}
	if account == "" || role == "" || region == "" || cacheKey == "" {
		return awsCredentials{}, "", fmt.Errorf("accounts: the [%s] profile's SSO settings are incomplete", name)
	}
	token, err := a.ssoToken(cacheKey)
	if err != nil {
		return awsCredentials{}, "", fmt.Errorf("%w; run `aws sso login --profile %s`", err, name)
	}
	q := url.Values{"account_id": {account}, "role_name": {role}}
	endpoint := base(a.ssoBase, "https://portal.sso."+region+".amazonaws.com") + "/federation/credentials"
	var out struct {
		RoleCredentials struct {
			AccessKeyID     string `json:"accessKeyId"`
			SecretAccessKey string `json:"secretAccessKey"`
			SessionToken    string `json:"sessionToken"`
		} `json:"roleCredentials"`
	}
	if err := doJSON(ctx, a.hc, http.MethodGet, endpoint+"?"+q.Encode(), http.Header{"x-amz-sso_bearer_token": {token}}, nil, &out); err != nil {
		if errors.Is(err, usage.ErrAuth) {
			return awsCredentials{}, "", fmt.Errorf("%w; run `aws sso login --profile %s`", err, name)
		}
		return awsCredentials{}, "", err
	}
	c := out.RoleCredentials
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return awsCredentials{}, "", fmt.Errorf("accounts: GET %s: unexpected answer", endpoint)
	}
	return awsCredentials{AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, SessionToken: c.SessionToken},
		"the [" + name + "] SSO profile (the sign-in `aws sso login` cached)", nil
}

// errSSOExpired is a cached SSO sign-in past its expiry.
var errSSOExpired = fmt.Errorf("%w (the AWS SSO sign-in has expired)", usage.ErrAuth)

// ssoToken reads the cached access token for an SSO session name, or a
// legacy profile's start URL, from ~/.aws/sso/cache/<sha1>.json.
func (a awsProfiles) ssoToken(key string) (string, error) {
	sum := sha1.Sum([]byte(key)) //nolint:gosec // the AWS CLI's cache file name
	path := filepath.Join(a.home, ".aws", "sso", "cache", hex.EncodeToString(sum[:])+".json")
	f, err := os.Open(path) // #nosec G304 -- the AWS CLI's own SSO cache, named as it names it
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w (no cached AWS SSO sign-in)", usage.ErrAuth)
		}
		return "", fmt.Errorf("accounts: read the AWS SSO cache: %w", errors.Unwrap(err))
	}
	defer func() { _ = f.Close() }()
	var cache struct {
		AccessToken string `json:"accessToken"`
		ExpiresAt   string `json:"expiresAt"`
	}
	if err := json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&cache); err != nil || cache.AccessToken == "" {
		return "", errors.New("accounts: the cached AWS SSO sign-in is not readable")
	}
	exp, ok := awsTime(cache.ExpiresAt)
	if !ok || !exp.After(a.now().Add(time.Minute)) {
		return "", errSSOExpired
	}
	return cache.AccessToken, nil
}

// awsTime reads the SSO cache's expiresAt: RFC 3339, or the older
// "2006-01-02T15:04:05UTC".
func awsTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05UTC", "2006-01-02T15:04:05Z0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
		return time.Unix(n, 0), true
	}
	return time.Time{}, false
}

func (a awsProfiles) client() *http.Client {
	if a.hc != nil {
		return a.hc
	}
	return &http.Client{Timeout: 20 * time.Second}
}
