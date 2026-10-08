package accounts

import (
	"context"
	"crypto/sha1" //nolint:gosec // the AWS CLI's cache file name
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// awsHome writes an AWS config file and SSO cache entries into a temporary
// home: nothing under the real ~/.aws is read.
func awsHome(t *testing.T, config string, cache map[string]string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".aws", "sso", "cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".aws", "config"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	for key, body := range cache {
		sum := sha1.Sum([]byte(key)) //nolint:gosec // the AWS CLI's cache file name
		if err := os.WriteFile(filepath.Join(dir, hex.EncodeToString(sum[:])+".json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// awsServer is the SSO portal and STS: role credentials for the cached
// token "sso-tok", and an assumed role for requests signed by AKIDSRC.
func awsServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/federation/credentials":
			seen = append(seen, "sso:"+r.URL.Query().Get("account_id")+"/"+r.URL.Query().Get("role_name"))
			if r.Header.Get("x-amz-sso_bearer_token") != "sso-tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"roleCredentials":{"accessKeyId":"ASIASSO","secretAccessKey":"ssosecret","sessionToken":"ssotoken","expiration":1893456000000}}`))
		case r.URL.Path == "/" && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			form, _ := url.ParseQuery(string(body))
			seen = append(seen, "sts:"+form.Get("Action")+":"+form.Get("RoleArn")+":"+form.Get("ExternalId"))
			if !strings.Contains(r.Header.Get("Authorization"), "Credential=AKIDSRC/") || !strings.Contains(r.Header.Get("Authorization"), "/sts/aws4_request") {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte(`<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleResult><Credentials><AccessKeyId>ASIAROLE</AccessKeyId><SecretAccessKey>rolesecret</SecretAccessKey><SessionToken>roletoken</SessionToken><Expiration>2030-01-01T00:00:00Z</Expiration></Credentials></AssumeRoleResult></AssumeRoleResponse>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

const awsConfig = `[profile sso]
sso_session = corp
sso_account_id = 111122223333
sso_role_name = Billing

[sso-session corp]
sso_start_url = https://corp.awsapps.com/start
sso_region = eu-central-1

[profile legacy]
sso_start_url = https://old.awsapps.com/start
sso_region = us-east-1
sso_account_id = 444455556666
sso_role_name = ReadOnly

[profile src]
aws_access_key_id = AKIDSRC
aws_secret_access_key = srcsecret

[profile role]
role_arn = arn:aws:iam::111122223333:role/CostReader
source_profile = src
external_id = ext-1

[profile mfa]
role_arn = arn:aws:iam::111122223333:role/Admin
source_profile = src
mfa_serial = arn:aws:iam::111122223333:mfa/me

[profile process]
credential_process = /usr/local/bin/aws-vault export --format=json work
`

func chainWith(t *testing.T, home, srvURL, profile string, now time.Time) (awsCredentials, string, error) {
	t.Helper()
	b := Bedrock{Home: home, SSOBaseURL: srvURL, STSBaseURL: srvURL, Now: func() time.Time { return now },
		Getenv: func(k string) string {
			if k == "AWS_PROFILE" {
				return profile
			}
			return ""
		}}
	key, origin, err := b.Chain(context.Background())
	var c awsCredentials
	if err == nil {
		_ = json.Unmarshal([]byte(key), &c)
	}
	return c, origin, err
}

func TestBedrockSSOProfiles(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	home := awsHome(t, awsConfig, map[string]string{
		"corp":                          `{"startUrl":"https://corp.awsapps.com/start","region":"eu-central-1","accessToken":"sso-tok","expiresAt":"2026-10-08T20:00:00Z"}`,
		"https://old.awsapps.com/start": `{"accessToken":"sso-tok","expiresAt":"2026-10-08T20:00:00UTC"}`,
	})
	srv, seen := awsServer(t)
	for _, profile := range []string{"sso", "legacy"} {
		c, origin, err := chainWith(t, home, srv.URL, profile, now)
		if err != nil || c.AccessKeyID != "ASIASSO" || c.SessionToken != "ssotoken" || !strings.Contains(origin, "SSO") {
			t.Errorf("%s: %+v %q %v", profile, c, origin, err)
		}
	}
	if len(*seen) != 2 || (*seen)[0] != "sso:111122223333/Billing" || (*seen)[1] != "sso:444455556666/ReadOnly" {
		t.Errorf("asked %v", *seen)
	}
	cache := filepath.Join(home, ".aws", "sso", "cache")
	entries, _ := os.ReadDir(cache)
	if len(entries) != 2 {
		t.Errorf("the SSO cache was written: %v", entries)
	}
}

// An expired cached sign-in is refused before anything is sent, with the
// hint to sign in again; it is never refreshed.
func TestBedrockExpiredSSOSignIn(t *testing.T) {
	home := awsHome(t, awsConfig, map[string]string{
		"corp": `{"accessToken":"sso-tok","expiresAt":"2026-10-08T11:00:00Z","refreshToken":"never-used"}`,
	})
	srv, seen := awsServer(t)
	_, _, err := chainWith(t, home, srv.URL, "sso", time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC))
	if !errors.Is(err, usage.ErrAuth) || !strings.Contains(err.Error(), "aws sso login --profile sso") || strings.Contains(err.Error(), "sso-tok") {
		t.Errorf("%v", err)
	}
	if len(*seen) != 0 {
		t.Errorf("asked %v", *seen)
	}
	// No cached sign-in at all is the same.
	if _, _, err := chainWith(t, awsHome(t, awsConfig, nil), srv.URL, "sso", time.Now()); !errors.Is(err, usage.ErrAuth) || !strings.Contains(err.Error(), "aws sso login") {
		t.Errorf("%v", err)
	}
}

// An assume-role profile asks STS with the source profile's keys.
func TestBedrockAssumeRoleProfile(t *testing.T) {
	home := awsHome(t, awsConfig, nil)
	srv, seen := awsServer(t)
	c, origin, err := chainWith(t, home, srv.URL, "role", time.Now())
	if err != nil || c.AccessKeyID != "ASIAROLE" || c.SessionToken != "roletoken" || !strings.Contains(origin, "[role]") || !strings.Contains(origin, "[src]") {
		t.Fatalf("%+v %q %v", c, origin, err)
	}
	if len(*seen) != 1 || (*seen)[0] != "sts:AssumeRole:arn:aws:iam::111122223333:role/CostReader:ext-1" {
		t.Errorf("asked %v", *seen)
	}
}

// Profiles that need a prompt or a program are refused, and nothing runs.
func TestBedrockProfilesThatCannotBeReadQuietly(t *testing.T) {
	home := awsHome(t, awsConfig, nil)
	srv, seen := awsServer(t)
	for _, profile := range []string{"mfa", "process"} {
		if _, _, err := chainWith(t, home, srv.URL, profile, time.Now()); !errors.Is(err, errAWSUnsupported) {
			t.Errorf("%s: %v", profile, err)
		}
	}
	if len(*seen) != 0 {
		t.Errorf("asked %v", *seen)
	}
}
