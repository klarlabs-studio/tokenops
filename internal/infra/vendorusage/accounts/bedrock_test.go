package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// AWS's Signature Version 4 test suite, get-vanilla: the documented
// signature for a GET of / on example.amazonaws.com.
func TestSignV4MatchesAWSTestSuite(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	signV4(req, nil, awsCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"},
		"us-east-1", "service", time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
		"SignedHeaders=host;x-amz-date, Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization\n got %s\nwant %s", got, want)
	}
}

func bedrockKey(t *testing.T) string {
	t.Helper()
	b, _ := json.Marshal(awsCredentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret", SessionToken: "tok"})
	return string(b)
}

// The fixture is a GetCostAndUsage answer grouped by service, as CodexBar's
// BedrockUsageStats parses it: every service named Bedrock is summed.
func TestBedrockSumsBedrockServices(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20261008/us-east-1/ce/aws4_request") ||
			r.Header.Get("X-Amz-Target") != "AWSInsightsIndexService.GetCostAndUsage" || r.Header.Get("X-Amz-Security-Token") != "tok" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"com.amazon.coral.service#UnrecognizedClientException"}`))
			return
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		_, _ = w.Write([]byte(fixture(t, "bedrock")))
	}))
	defer srv.Close()
	b := Bedrock{BaseURL: srv.URL, Now: octoberEighth}
	got, err := b.Read(context.Background(), bedrockKey(t))
	if err != nil || !got.HasUsed || !approx(got.UsedUSD, 50) || got.LimitUSD != 0 || got.Subscription {
		t.Fatalf("got %+v, %v", got, err)
	}
	period, _ := body["TimePeriod"].(map[string]any)
	if period["Start"] != "2026-10-01" || period["End"] != "2026-10-09" || body["Granularity"] != "MONTHLY" {
		t.Errorf("request %v", body)
	}
	if b.MinInterval() < 6*time.Hour {
		t.Error("Cost Explorer bills each request: it is asked a few times a day at most")
	}
}

func TestBedrockRefusalsAndNoData(t *testing.T) {
	answer := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()
	b := Bedrock{BaseURL: srv.URL, Now: octoberEighth}
	for _, refused := range []string{`{"__type":"AccessDeniedException","Message":"no ce:GetCostAndUsage"}`, `{"__type":"ExpiredTokenException"}`} {
		answer = refused
		if _, err := b.Read(context.Background(), bedrockKey(t)); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%s: %v", refused, err)
		}
	}
	answer = `{"__type":"DataUnavailableException"}`
	if got, err := b.Read(context.Background(), bedrockKey(t)); err != nil || !got.HasUsed || got.UsedUSD != 0 {
		t.Errorf("no data yet: %+v, %v", got, err)
	}
	answer = `{"__type":"ThrottlingException"}`
	if _, err := b.Read(context.Background(), bedrockKey(t)); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("throttled: %v", err)
	}
	if _, err := b.Read(context.Background(), "not json"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("not a credential: %v", err)
	}
}

func TestBedrockFollowsPages(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"NextPageToken":"p2"`) {
			_, _ = w.Write([]byte(`{"ResultsByTime":[{"Groups":[{"Keys":["Amazon Bedrock"],"Metrics":{"UnblendedCost":{"Amount":"1.5","Unit":"USD"}}}]}],"NextPageToken":"p2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ResultsByTime":[{"Groups":[{"Keys":["Amazon Bedrock AgentCore"],"Metrics":{"UnblendedCost":{"Amount":"2","Unit":"USD"}}}]}]}`))
	}))
	defer srv.Close()
	got, err := Bedrock{BaseURL: srv.URL, Now: octoberEighth}.Read(context.Background(), bedrockKey(t))
	if err != nil || calls != 2 || !approx(got.UsedUSD, 3.5) {
		t.Fatalf("got %+v, %v after %d", got, err, calls)
	}
}

// The credential chain: the environment first, then the shared
// credentials file's profile; never the AWS CLI.
func TestBedrockCredentialChain(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".aws"), 0o700); err != nil {
		t.Fatal(err)
	}
	file := "[default]\naws_access_key_id = AKIDDEFAULT\naws_secret_access_key = s1\n\n# work\n[work]\naws_access_key_id=AKIDWORK\naws_secret_access_key=s2\naws_session_token=t2\n[sso]\nsso_session = corp\n"
	if err := os.WriteFile(filepath.Join(home, ".aws", "credentials"), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	chain := func(env map[string]string) (awsCredentials, string, error) {
		key, origin, err := Bedrock{Home: home, Getenv: func(k string) string { return env[k] }}.Chain(context.Background())
		var c awsCredentials
		if err == nil {
			_ = json.Unmarshal([]byte(key), &c)
		}
		return c, origin, err
	}
	if c, origin, err := chain(map[string]string{"AWS_ACCESS_KEY_ID": "AKIDENV", "AWS_SECRET_ACCESS_KEY": "s0"}); err != nil || c.AccessKeyID != "AKIDENV" || origin != "$AWS_ACCESS_KEY_ID" {
		t.Errorf("environment: %+v %q %v", c, origin, err)
	}
	if c, _, err := chain(nil); err != nil || c.AccessKeyID != "AKIDDEFAULT" || c.SecretAccessKey != "s1" {
		t.Errorf("default profile: %+v %v", c, err)
	}
	if c, origin, err := chain(map[string]string{"AWS_PROFILE": "work"}); err != nil || c.AccessKeyID != "AKIDWORK" || c.SessionToken != "t2" || !strings.Contains(origin, "[work]") {
		t.Errorf("named profile: %+v %q %v", c, origin, err)
	}
	if _, _, err := chain(map[string]string{"AWS_PROFILE": "sso"}); !errors.Is(err, errNoAWSCredentials) {
		t.Errorf("an SSO profile is not read: %v", err)
	}
	if _, _, err := (Bedrock{Home: t.TempDir(), Getenv: func(string) string { return "" }}).Chain(context.Background()); !errors.Is(err, errNoAWSCredentials) {
		t.Errorf("no credentials: %v", err)
	}
}
