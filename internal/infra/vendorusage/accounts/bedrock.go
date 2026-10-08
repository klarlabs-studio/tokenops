package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerBedrock registers the reader (readers_gen.go).
func readerBedrock() usage.Reader { return Bedrock{} }

// Bedrock reads this month's Amazon Bedrock spend from AWS Cost Explorer
// (GetCostAndUsage, grouped by service), as CodexBar's Bedrock provider
// does, signed with the AWS credentials on this machine. Cost Explorer
// bills each request ($0.01 at the time of writing), so it is asked at
// most every bedrockInterval, and only for an operator who opted in with
// `tokenops vendor-usage setup bedrock`.
//
// The credentials are AWS's standard environment variables, else the
// profile AWS_PROFILE names (or default): its static keys, an SSO profile's
// cached sign-in, or an assume-role profile through STS, all without the
// AWS CLI and without a prompt. credential_process and MFA profiles are
// not read (awsProfiles says why).
type Bedrock struct {
	BaseURL string
	HTTP    *http.Client
	// Now, Getenv and Home default to the real clock, environment and home
	// directory.
	Now    func() time.Time
	Getenv func(string) string
	Home   string
	// SSOBaseURL and STSBaseURL override the SSO portal's and STS's
	// addresses, for tests.
	SSOBaseURL, STSBaseURL string
}

// Endpoint is its own: only the credential chain setup opted in to is
// read here, never a key a harness sends Bedrock.
func (Bedrock) Endpoint() string               { return "bedrock-cost-explorer" }
func (Bedrock) Provider() eventschema.Provider { return "bedrock" }
func (Bedrock) Source() string                 { return "bedrock-cost-explorer" }

// bedrockInterval is how often Cost Explorer is asked: its figures update
// a few times a day, and each request is billed.
const bedrockInterval = 8 * time.Hour

// MinInterval paces the poller (usage.Paced).
func (Bedrock) MinInterval() time.Duration { return bedrockInterval }

// errNoAWSCredentials is no AWS credential on this machine.
var errNoAWSCredentials = errors.New("accounts: no AWS credentials in the environment or the AWS profile")

// Chain finds AWS credentials the way the AWS SDKs start, without the AWS
// CLI and without a prompt (usage.ChainReader): the environment's keys, else
// the profile AWS_PROFILE names (or default) in the shared config and
// credentials files: an assume-role profile through STS, an SSO profile
// through the sign-in `aws sso login` cached, or its static keys
// (awsProfiles). The key it returns is for Read only, never stored or shown.
func (b Bedrock) Chain(ctx context.Context) (string, string, error) {
	getenv := b.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	c := awsCredentials{
		AccessKeyID:     strings.TrimSpace(getenv("AWS_ACCESS_KEY_ID")),
		SecretAccessKey: strings.TrimSpace(getenv("AWS_SECRET_ACCESS_KEY")),
		SessionToken:    strings.TrimSpace(getenv("AWS_SESSION_TOKEN")),
	}
	origin := "$AWS_ACCESS_KEY_ID"
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		home := b.Home
		if home == "" {
			var err error
			if home, err = os.UserHomeDir(); err != nil {
				return "", "", err
			}
		}
		profile := strings.TrimSpace(getenv("AWS_PROFILE"))
		if profile == "" {
			profile = "default"
		}
		now := time.Now
		if b.Now != nil {
			now = b.Now
		}
		var err error
		c, origin, err = awsProfiles{home: home, getenv: getenv, hc: b.HTTP, ssoBase: b.SSOBaseURL, stsBase: b.STSBaseURL, now: now}.credentials(ctx, profile)
		if err != nil {
			return "", "", err
		}
	}
	b2, err := json.Marshal(c)
	if err != nil {
		return "", "", err
	}
	return string(b2), origin, nil
}

// bedrockPages bounds the pages followed, as CodexBar stops on a repeated
// token.
const bedrockPages = 20

func (b Bedrock) Read(ctx context.Context, key string) (usage.Reading, error) {
	var c awsCredentials
	if err := json.Unmarshal([]byte(key), &c); err != nil || c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return usage.Reading{}, fmt.Errorf("%w (not AWS credentials)", usage.ErrAuth)
	}
	now := time.Now
	if b.Now != nil {
		now = b.Now
	}
	t := now().UTC()
	// Cost Explorer's End is exclusive: through today.
	start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
	var (
		total float64
		token string
		seen  = map[string]bool{}
	)
	for i := 0; ; i++ {
		if i == bedrockPages {
			return usage.Reading{}, errors.New("accounts: Cost Explorer: too many pages")
		}
		page, err := b.costPage(ctx, c, start, end, token, t)
		if err != nil {
			return usage.Reading{}, err
		}
		for _, r := range page.ResultsByTime {
			for _, g := range r.Groups {
				if len(g.Keys) == 0 || !strings.Contains(strings.ToLower(g.Keys[0]), "bedrock") {
					continue
				}
				m := g.Metrics["UnblendedCost"]
				if m.Unit != "" && m.Unit != "USD" {
					return usage.Reading{}, fmt.Errorf("accounts: Cost Explorer reports %s, not USD", m.Unit)
				}
				if v, err := strconv.ParseFloat(m.Amount, 64); err == nil {
					total += v
				}
			}
		}
		if page.NextPageToken == "" {
			break
		}
		if seen[page.NextPageToken] {
			return usage.Reading{}, errors.New("accounts: Cost Explorer repeated its page token")
		}
		seen[page.NextPageToken] = true
		token = page.NextPageToken
	}
	// Month-to-date spend, with no cap: AWS reports none here.
	return usage.Reading{Scope: "account", UsedUSD: total, HasUsed: true}, nil
}

type costExplorerPage struct {
	ResultsByTime []struct {
		Groups []struct {
			Keys    []string `json:"Keys"`
			Metrics map[string]struct {
				Amount string `json:"Amount"`
				Unit   string `json:"Unit"`
			} `json:"Metrics"`
		} `json:"Groups"`
	} `json:"ResultsByTime"`
	NextPageToken string `json:"NextPageToken"`
}

// costPage asks GetCostAndUsage for one page of the month's spend by
// service. Cost Explorer's endpoint is global, signed for us-east-1.
func (b Bedrock) costPage(ctx context.Context, c awsCredentials, start, end time.Time, token string, now time.Time) (costExplorerPage, error) {
	req := map[string]any{
		"TimePeriod":  map[string]string{"Start": start.Format("2006-01-02"), "End": end.Format("2006-01-02")},
		"Granularity": "MONTHLY",
		"Metrics":     []string{"UnblendedCost"},
		"GroupBy":     []map[string]string{{"Type": "DIMENSION", "Key": "SERVICE"}},
	}
	if token != "" {
		req["NextPageToken"] = token
	}
	body, err := json.Marshal(req)
	if err != nil {
		return costExplorerPage{}, err
	}
	url := base(b.BaseURL, "https://ce.us-east-1.amazonaws.com") + "/"
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return costExplorerPage{}, err
	}
	r.Header.Set("Content-Type", "application/x-amz-json-1.1")
	r.Header.Set("X-Amz-Target", "AWSInsightsIndexService.GetCostAndUsage")
	signV4(r, body, c, "us-east-1", "ce", now)
	hc := b.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(r)
	if err != nil {
		return costExplorerPage{}, fmt.Errorf("accounts: POST %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		switch awsErrorType(raw) {
		case "DataUnavailableException":
			// No cost data yet this month: nothing spent, as CodexBar reads it.
			return costExplorerPage{}, nil
		case "UnrecognizedClientException", "InvalidSignatureException", "ExpiredTokenException",
			"AccessDeniedException", "InvalidClientTokenId", "SignatureDoesNotMatch":
			return costExplorerPage{}, fmt.Errorf("%w (%d on %s)", usage.ErrAuth, resp.StatusCode, url)
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return costExplorerPage{}, fmt.Errorf("%w (%d on %s)", usage.ErrAuth, resp.StatusCode, url)
		}
		return costExplorerPage{}, fmt.Errorf("accounts: POST %s: status %d", url, resp.StatusCode)
	}
	var page costExplorerPage
	if err := json.Unmarshal(raw, &page); err != nil {
		return costExplorerPage{}, fmt.Errorf("accounts: POST %s: %w", url, err)
	}
	return page, nil
}

// awsErrorType is the error code in an AWS JSON error ("__type", maybe
// prefixed with a namespace and "#").
func awsErrorType(body []byte) string {
	var e struct {
		Type string `json:"__type"`
		Code string `json:"code"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	t := e.Type
	if t == "" {
		t = e.Code
	}
	if i := strings.LastIndex(t, "#"); i >= 0 {
		t = t[i+1:]
	}
	return t
}
