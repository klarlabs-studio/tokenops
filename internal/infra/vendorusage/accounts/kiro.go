package accounts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerKiro registers the reader (readers_gen.go).
func readerKiro() usage.Reader { return Kiro{} }

// Kiro reads Kiro's monthly credits by asking the operator's kiro-cli:
// `kiro-cli chat --no-interactive /usage`, which signs its own request
// with the AWS Builder ID it keeps (TokenOps never sees it). `kiro-cli
// whoami` runs first: a CLI that is not signed in is never asked for
// usage, so nothing starts a sign-in from the background. The output is
// a terminal report, parsed as CodexBar parses it.
//
// The overage credits, which only Kiro's private GetUsageLimits API
// reports, are KiroOverage's, with the token kiro-cli keeps in its own
// database, read only once the operator granted it (ADR 0013).
type Kiro struct {
	// Bin overrides finding kiro-cli (KIRO_CLI_PATH, PATH, install
	// directories); Now the clock. For tests.
	Bin string
	Now func() time.Time
}

func (Kiro) Endpoint() string               { return "kiro" }
func (Kiro) Provider() eventschema.Provider { return "kiro" }
func (Kiro) Source() string                 { return "kiro-cli" }
func (Kiro) Keyless()                       {}

// kiroSignedOut is how kiro-cli says it is not signed in.
var kiroSignedOut = []string{"not logged in", "login required", "failed to initialize auth portal", "kiro-cli login", "oauth error"}

// kiroEnv is the terminal kiro-cli renders its report for.
var kiroEnv = []string{"TERM=xterm-256color", "LANG=en_US.UTF-8"}

func (k Kiro) Read(ctx context.Context, _ string) (usage.Reading, error) {
	bin, ok := k.Bin, k.Bin != ""
	if !ok {
		bin, ok = locateCLI("kiro-cli", strings.TrimSpace(os.Getenv("KIRO_CLI_PATH")))
	}
	if !ok {
		return usage.Reading{}, usage.ErrNotInstalled
	}
	who, err := runCLI(ctx, 5*time.Second, bin, kiroEnv, "whoami")
	if mentions(who, kiroSignedOut...) {
		return usage.Reading{}, cliAuthError("kiro-cli")
	}
	if err != nil && !errors.Is(err, errCLIFailed) {
		return usage.Reading{}, err
	}
	out, err := runCLI(ctx, 0, bin, kiroEnv, "chat", "--no-interactive", "/usage")
	if mentions(out, kiroSignedOut...) {
		return usage.Reading{}, cliAuthError("kiro-cli")
	}
	if err != nil {
		return usage.Reading{}, err
	}
	now := time.Now
	if k.Now != nil {
		now = k.Now
	}
	return parseKiroUsage(out, now())
}

var (
	kiroSummaryPlan = regexp.MustCompile(`(?m)^[ \t]*Plan:[ \t]*([^|\r\n]+?)[ \t]*\|[ \t]*[0-9]+[ \t]+usage breakdowns?[ \t]*$`)
	kiroPlanLine    = regexp.MustCompile(`Plan:[ \t]*(.+)`)
	kiroPercent     = regexp.MustCompile(`█+\s*(\d+)%`)
	kiroCredits     = regexp.MustCompile(`\((\d+\.?\d*)\s+of\s+(\d+)\s+covered`)
	kiroBonus       = regexp.MustCompile(`Bonus credits:\s*(\d+\.?\d*)/(\d+)`)
	kiroBonusExpiry = regexp.MustCompile(`expires in (\d+) days?`)
	kiroReset       = regexp.MustCompile(`resets on (\d{4}-\d{2}-\d{2}|\d{2}/\d{2})`)
)

// parseKiroUsage maps the /usage report: the plan's monthly credits (the
// bar's percentage, or credits used of those covered in the plan), reset
// on the date printed, and bonus credits, which expire rather than reset.
// A plan-only report (managed accounts, kiro-cli 2.20's summary) has no
// figures and reads as empty.
func parseKiroUsage(text string, now time.Time) (usage.Reading, error) {
	if strings.TrimSpace(text) == "" || mentions(text, "could not retrieve usage information") {
		return usage.Reading{}, fmt.Errorf("accounts: kiro-cli reported no usage")
	}
	lower := strings.ToLower(text)
	summary := kiroSummaryPlan.MatchString(text)
	newFormat := summary || kiroPlanLine.MatchString(text)
	managed := strings.Contains(lower, "managed by admin") || strings.Contains(lower, "managed by organization")

	used, havePct := 0.0, false
	if m := kiroPercent.FindStringSubmatch(text); m != nil {
		used, _ = strconv.ParseFloat(m[1], 64)
		havePct = true
	}
	haveCredits := false
	if m := kiroCredits.FindStringSubmatch(text); m != nil {
		haveCredits = true
		if !havePct {
			u, _ := strconv.ParseFloat(m[1], 64)
			total, _ := strconv.ParseFloat(m[2], 64)
			used = pct(u, total)
		}
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	if !havePct && !haveCredits {
		if newFormat && (managed || summary) {
			return r, nil
		}
		return usage.Reading{}, fmt.Errorf("accounts: kiro-cli usage in a shape this version cannot read")
	}
	r.Windows = append(r.Windows, usage.Window{
		Name: "month", UsedPct: used, Duration: 30 * 24 * time.Hour, ResetsAt: kiroResetDate(text, now),
	})
	if m := kiroBonus.FindStringSubmatch(text); m != nil {
		u, _ := strconv.ParseFloat(m[1], 64)
		total, _ := strconv.ParseFloat(m[2], 64)
		if total > 0 {
			w := usage.Window{Name: "bonus credits", UsedPct: pct(u, total)}
			if e := kiroBonusExpiry.FindStringSubmatch(text); e != nil {
				days, _ := strconv.Atoi(e[1])
				w.ResetsAt = now.Add(time.Duration(days) * 24 * time.Hour).UTC()
			}
			r.Windows = append(r.Windows, w)
		}
	}
	return r, nil
}

// kiroResetDate is the printed reset, at local midnight: a full date, or
// MM/DD in the current year if still ahead, else the next.
func kiroResetDate(text string, now time.Time) time.Time {
	m := kiroReset.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}
	}
	if t, err := time.ParseInLocation("2006-01-02", m[1], time.Local); err == nil {
		return t.UTC()
	}
	t, err := time.ParseInLocation("01/02", m[1], time.Local)
	if err != nil {
		return time.Time{}
	}
	t = time.Date(now.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	if !t.After(now) {
		t = t.AddDate(1, 0, 0)
	}
	return t.UTC()
}
