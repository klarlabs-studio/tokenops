package accounts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerCodeRabbit registers the reader (readers_gen.go).
func readerCodeRabbit() usage.Reader { return CodeRabbit{} }

// CodeRabbit reads the reviews of the current billing period from the
// operator's own CodeRabbit CLI (`coderabbit usage`, documented at
// docs.coderabbit.ai/cli/reference#usage-command), which signs its own
// request with the login it keeps. The report has no allowance to measure
// the reviews against, so they are a count, not a percentage.
type CodeRabbit struct {
	// Bin overrides finding coderabbit (CODERABBIT_CLI_PATH, PATH,
	// install directories), for tests.
	Bin string
}

func (CodeRabbit) Endpoint() string               { return "coderabbit" }
func (CodeRabbit) Provider() eventschema.Provider { return "coderabbit" }
func (CodeRabbit) Source() string                 { return "coderabbit-cli" }
func (CodeRabbit) Keyless()                       {}

// coderabbitSignedOut is how the CLI says it has no hosted login.
var coderabbitSignedOut = []string{"not authenticated", "please log in", "auth login", "authentication required", "unauthorized", "no session found"}

func (c CodeRabbit) Read(ctx context.Context, _ string) (usage.Reading, error) {
	bin, ok := c.Bin, c.Bin != ""
	if !ok {
		bin, ok = locateCLI("coderabbit", strings.TrimSpace(os.Getenv("CODERABBIT_CLI_PATH")))
	}
	if !ok {
		return usage.Reading{}, usage.ErrNotInstalled
	}
	out, err := runCLI(ctx, 15*time.Second, bin, nil, "usage")
	if err != nil {
		if errors.Is(err, errCLIFailed) && mentions(out, coderabbitSignedOut...) {
			return usage.Reading{}, cliAuthError("coderabbit")
		}
		return usage.Reading{}, err
	}
	return parseCodeRabbitUsage(out)
}

// parseCodeRabbitUsage reads the report's "Label : value" lines: the
// reviews this billing period and when the period resets.
func parseCodeRabbitUsage(text string) (usage.Reading, error) {
	fields := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		label, value, ok := strings.Cut(line, ":")
		label, value = strings.ToLower(strings.TrimSpace(label)), strings.TrimSpace(value)
		if !ok || value == "" {
			continue
		}
		if _, seen := fields[label]; !seen {
			fields[label] = value
		}
	}
	reviews, haveReviews := -1.0, false
	if v, ok := fields["your reviews"]; ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			reviews, haveReviews = float64(n), true
		}
	}
	_, billing := fields["usage billing"]
	resets, haveReset := fields["period resets"]
	if !haveReviews && !billing && !haveReset {
		if mentions(text, coderabbitSignedOut...) {
			return usage.Reading{}, cliAuthError("coderabbit")
		}
		return usage.Reading{}, fmt.Errorf("accounts: coderabbit usage in a shape this version cannot read")
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	if haveReviews {
		r.Counts = append(r.Counts, usage.Count{Name: "reviews", Used: reviews, ResetsAt: coderabbitReset(resets)})
	}
	return r, nil
}

// coderabbitReset reads "2026-09-30" or "2026-09-30 14:30:00", local time.
func coderabbitReset(s string) time.Time {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
