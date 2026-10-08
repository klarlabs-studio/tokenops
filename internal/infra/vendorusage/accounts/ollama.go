package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerOllama registers the reader (readers_gen.go).
func readerOllama() usage.Reader { return Ollama{} }

// Ollama reads Ollama Cloud's usage windows from ollama.com/settings with
// the browser session, as CodexBar's Ollama provider does
// (Sources/CodexBarCore/Providers/Ollama/OllamaUsageFetcher.swift,
// OllamaUsageParser.swift): there is no usage API, so the page is read.
// The current page has a monthly window (or the free plan's), the older
// one a session (or hourly) and a weekly window. A redirect to the sign-in
// page, or a sign-in form, is an expired session.
type Ollama struct {
	BaseURL string
	HTTP    *http.Client
}

func (Ollama) Endpoint() string               { return "ollama" }
func (Ollama) Provider() eventschema.Provider { return "ollama" }
func (Ollama) Source() string                 { return "ollama-web" }

// ollamaLabels are the page's usage blocks.
var ollamaLabels = []string{"Monthly usage", "Free usage", "Session usage", "Hourly usage", "Weekly usage"}

var (
	ollamaPctUsed  = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*%\s*used`)
	ollamaAmount   = `((?:[0-9]{1,3}(?:,[0-9]{3})+|[0-9]+)(?:\.[0-9]+)?)`
	ollamaDollars  = regexp.MustCompile(`\$` + ollamaAmount + `\s+of\s+\$` + ollamaAmount + `\s+used`)
	ollamaWidth    = regexp.MustCompile(`width:\s*([0-9]+(?:\.[0-9]+)?)%`)
	ollamaDataTime = regexp.MustCompile(`data-time="([^"]+)"`)
)

func (o Ollama) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	cookie = cookieHeader(cookie)
	if !isSession(cookie) {
		return usage.Reading{}, fmt.Errorf("%w (Ollama Cloud is read with ollama.com's session cookie)", usage.ErrAuth)
	}
	root := base(o.BaseURL, "https://ollama.com")
	page, err := doWeb(ctx, o.HTTP, http.MethodGet, root+"/settings", http.Header{
		"Cookie":          {cookie},
		"Accept":          {"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
		"Accept-Language": {"en-US,en;q=0.9"},
		"Referer":         {"https://ollama.com/settings"},
	}, nil)
	if err != nil {
		return usage.Reading{}, err
	}
	return ollamaReading(string(page))
}

func ollamaReading(html string) (usage.Reading, error) {
	r := usage.Reading{Scope: "account", Subscription: true}
	add := func(labels []string, name string, d time.Duration) {
		for _, label := range labels {
			if block, ok := ollamaBlock(html, label); ok {
				if used, ok := ollamaPercent(block); ok {
					w := usage.Window{Name: name, UsedPct: clampPct(used), Duration: d}
					if m := ollamaDataTime.FindStringSubmatch(block); m != nil {
						w.ResetsAt = parseTime(m[1])
					}
					r.Windows = append(r.Windows, w)
				}
				return
			}
		}
	}
	add([]string{"Monthly usage", "Free usage"}, "month", 0)
	if len(r.Windows) == 0 {
		if _, ok := ollamaBlock(html, "Session usage"); ok {
			add([]string{"Session usage"}, "5h", 5*time.Hour)
		} else {
			add([]string{"Hourly usage"}, "hour", 0)
		}
	}
	add([]string{"Weekly usage"}, "week", 7*24*time.Hour)
	if len(r.Windows) > 0 {
		return r, nil
	}
	if ollamaSignedOut(html) {
		return usage.Reading{}, fmt.Errorf("%w (ollama.com/settings shows the sign-in page)", usage.ErrAuth)
	}
	return usage.Reading{}, errors.New("accounts: GET ollama.com/settings: no usage on the page")
}

// ollamaBlock is the page from a label to the next usage label, at most
// 4000 bytes.
func ollamaBlock(html, label string) (string, bool) {
	at := regexp.MustCompile(`>\s*` + regexp.QuoteMeta(label) + `\s*<`).FindStringIndex(html)
	if at == nil {
		return "", false
	}
	rest := html[at[1]:]
	end := min(len(rest), 4000)
	for _, other := range ollamaLabels {
		if other == label {
			continue
		}
		if i := strings.Index(rest[:end], other); i >= 0 {
			end = i
		}
	}
	return rest[:end], true
}

func ollamaPercent(block string) (float64, bool) {
	if m := ollamaPctUsed.FindStringSubmatch(block); m != nil {
		v, err := strconv.ParseFloat(m[1], 64)
		return v, err == nil
	}
	if m := ollamaDollars.FindStringSubmatch(block); m != nil {
		used, err1 := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
		limit, err2 := strconv.ParseFloat(strings.ReplaceAll(m[2], ",", ""), 64)
		if err1 == nil && err2 == nil && limit > 0 {
			return pct(used, limit), true
		}
	}
	if m := ollamaWidth.FindStringSubmatch(block); m != nil {
		v, err := strconv.ParseFloat(m[1], 64)
		return v, err == nil
	}
	return 0, false
}

// ollamaSignedOut recognises the sign-in page CodexBar recognises: a form
// with an email and a password field, or one posting to a sign-in route.
func ollamaSignedOut(html string) bool {
	t := strings.ToLower(html)
	if !strings.Contains(t, "<form") {
		return false
	}
	authRoute := strings.Contains(t, "/api/auth/signin") || strings.Contains(t, "/auth/signin") ||
		strings.Contains(t, `action="/login"`) || strings.Contains(t, `action="/signin"`) ||
		strings.Contains(t, `href="/login"`) || strings.Contains(t, `href="/signin"`)
	fields := strings.Contains(t, `type="password"`) && strings.Contains(t, `type="email"`)
	return authRoute || fields
}
