package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerMuseAI registers the reader (readers_gen.go).
func readerMuseAI() usage.Reader { return MuseAI{} }

// MuseAI reads the weekly token allowance of a muse.ai plan (Free, Power,
// Maximum) with muse.ai's browser session, as CodexBar's MuseAI provider
// does (Sources/CodexBarCore/Resources/Plugins/museai.js, docs/museai.md).
// muse.ai has no usage API: the page's own server action,
// fetchSubscriptionAction, answers it. Its ID changes with every deploy,
// so it is found in the page's script chunks and kept until muse.ai says
// it is gone. Only the share used is published, not the tokens.
type MuseAI struct {
	BaseURL string
	HTTP    *http.Client
}

func (MuseAI) Endpoint() string               { return "museai" }
func (MuseAI) Provider() eventschema.Provider { return "museai" }
func (MuseAI) Source() string                 { return "museai-web" }

// museAIAction is the server action's ID last found, per site.
var museAIAction = struct {
	sync.Mutex
	id map[string]string
}{id: map[string]string{}}

var (
	museAIFlightRow  = regexp.MustCompile(`^[0-9a-f]+:(\{.*\})\s*$`)
	museAIChunk      = regexp.MustCompile(`static/chunks/[\w~.-]+\.js`)
	museAISettings   = regexp.MustCompile(`\.A\((\d+)\)\.then\(\(\{[^}]*Settings`)
	museAIActionID   = regexp.MustCompile(`"([0-9a-f]{40,})",[^"]{0,200}"fetchSubscriptionAction"`)
	errMuseAIStale   = errors.New("server action not found")
	museAIMaxScripts = 8 << 20
)

func (m MuseAI) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	cookie = cookieHeader(cookie)
	if !isSession(cookie) || cookieValue(cookie, "hatch_sess") == "" {
		return usage.Reading{}, fmt.Errorf("%w (muse.ai is read with its hatch_sess cookie)", usage.ErrAuth)
	}
	root := base(m.BaseURL, "https://muse.ai")
	museAIAction.Lock()
	id := museAIAction.id[root]
	museAIAction.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		if id == "" {
			var err error
			if id, err = m.discover(ctx, root, cookie); err != nil {
				return usage.Reading{}, err
			}
		}
		r, err := m.fetch(ctx, root, cookie, id)
		if errors.Is(err, errMuseAIStale) {
			id = ""
			continue
		}
		if err != nil {
			return usage.Reading{}, err
		}
		museAIAction.Lock()
		museAIAction.id[root] = id
		museAIAction.Unlock()
		return r, nil
	}
	museAIAction.Lock()
	delete(museAIAction.id, root)
	museAIAction.Unlock()
	return usage.Reading{}, errors.New("accounts: POST muse.ai: no working subscription action")
}

func (m MuseAI) fetch(ctx context.Context, root, cookie, id string) (usage.Reading, error) {
	body, err := doWeb(ctx, m.HTTP, http.MethodPost, root+"/", http.Header{
		"Cookie": {cookie}, "Accept": {"text/x-component"}, "Content-Type": {"application/json"},
		"Next-Action": {id}, "Origin": {"https://muse.ai"},
		"Sec-Fetch-Site": {"same-origin"}, "Sec-Fetch-Mode": {"cors"}, "Sec-Fetch-Dest": {"empty"},
	}, []byte(`[{"includeAgreement":true}]`))
	if err != nil {
		var se *statusError
		if errors.As(err, &se) && se.status == http.StatusNotFound && strings.Contains(strings.ToLower(string(se.body)), "server action not found") {
			return usage.Reading{}, errMuseAIStale
		}
		return usage.Reading{}, err
	}
	for _, line := range strings.Split(string(body), "\n") {
		m := museAIFlightRow.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		var row struct {
			Success      bool `json:"success"`
			Subscription *struct {
				Usage *struct {
					PercentUsed *float64 `json:"percentUsed"`
					ResetsAt    stamp    `json:"resetsAt"`
				} `json:"usage"`
			} `json:"subscription"`
		}
		if json.Unmarshal([]byte(m[1]), &row) != nil || !row.Success {
			continue
		}
		if row.Subscription == nil || row.Subscription.Usage == nil || row.Subscription.Usage.PercentUsed == nil {
			break
		}
		u := row.Subscription.Usage
		return usage.Reading{Scope: "account", Subscription: true, Windows: []usage.Window{{
			Name: "week", UsedPct: clampPct(*u.PercentUsed), Duration: 7 * 24 * time.Hour, ResetsAt: u.ResetsAt.t,
		}}}, nil
	}
	return usage.Reading{}, errors.New("accounts: POST muse.ai: unexpected subscription answer")
}

// discover finds fetchSubscriptionAction's ID: the page names its script
// chunks; one of them loads the settings module, whose own chunks hold
// the action.
func (m MuseAI) discover(ctx context.Context, root, cookie string) (string, error) {
	page, err := doWeb(ctx, m.HTTP, http.MethodGet, root+"/", http.Header{"Cookie": {cookie}, "Accept": {"text/html"}}, nil)
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(page), "static/chunks/") && looksLikeSignIn(page) {
		return "", fmt.Errorf("%w (muse.ai shows the sign-in page)", usage.ErrAuth)
	}
	scripts, err := m.scripts(ctx, root, museAIChunk.FindAllString(string(page), -1), 96)
	if err != nil {
		return "", err
	}
	mod := museAISettings.FindStringSubmatch(scripts)
	if mod == nil {
		return "", errors.New("accounts: muse.ai: no settings module in the page's scripts")
	}
	loader := regexp.MustCompile(`[,{\[]` + mod[1] + `,\w+=>\{\w+\.v\(\w+=>Promise\.all\(\[([^\]]*)`).FindStringSubmatch(scripts)
	if loader == nil {
		return "", errors.New("accounts: muse.ai: no loader for the settings module")
	}
	settings, err := m.scripts(ctx, root, museAIChunk.FindAllString(loader[1], -1), 32)
	if err != nil {
		return "", err
	}
	id := museAIActionID.FindStringSubmatch(settings)
	if id == nil {
		return "", errors.New("accounts: muse.ai: no subscription action in the settings chunks")
	}
	return id[1], nil
}

// scripts fetches up to limit distinct chunks, without the session, and
// joins them; a chunk that does not load is empty.
func (m MuseAI) scripts(ctx context.Context, root string, paths []string, limit int) (string, error) {
	seen := map[string]bool{}
	var b strings.Builder
	for _, p := range paths {
		if seen[p] || len(seen) >= limit {
			continue
		}
		seen[p] = true
		text := museAIScript(ctx, m.HTTP, root+"/_next/"+p)
		if b.Len()+len(text) > museAIMaxScripts {
			return "", errors.New("accounts: muse.ai: the page's scripts are too large")
		}
		b.WriteString(text)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func museAIScript(ctx context.Context, hc *http.Client, u string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", browserUA)
	resp, err := noRedirect(hc).Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, int64(museAIMaxScripts)))
	return string(b)
}
