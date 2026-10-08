package accounts

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerAntigravity registers the reader (readers_gen.go).
func readerAntigravity() usage.Reader { return Antigravity{} }

// Antigravity reads Google Antigravity's model quota from the language
// server the running Antigravity app (or IDE) starts on this machine: the
// same local RetrieveUserQuotaSummary call its Model Quota view makes,
// over HTTPS on 127.0.0.1, with the CSRF token the app passes the server
// on its command line. Nothing leaves the machine; the token is read from
// the process list, used for that one local call and never stored or
// logged. With the app closed there is nothing to read. This follows
// CodexBar's local probe; the protocol is Antigravity's own, unpublished.
//
// Not read: the agy CLI's server (agy 1.2.2 and later refuse a request
// without a CSRF token it does not expose) and Google's OAuth quota API,
// which would need Antigravity's Google sign-in.
type Antigravity struct {
	// Processes and Ports replace the process and socket listing, and
	// HTTP the loopback client, for tests.
	Processes func(ctx context.Context) ([]antigravityProcess, error)
	Ports     func(ctx context.Context, pid int) ([]int, error)
	HTTP      *http.Client
}

func (Antigravity) Endpoint() string               { return "antigravity" }
func (Antigravity) Provider() eventschema.Provider { return "antigravity" }
func (Antigravity) Source() string                 { return "antigravity-local" }
func (Antigravity) Keyless()                       {}

// antigravityProcess is a process and its command line.
type antigravityProcess struct {
	pid     int
	command string
}

var (
	agLanguageServer = regexp.MustCompile(`(^|[/\\])language(?:_|-)server(?:[_-][a-z0-9]+)*(?:\.exe)?(\s|$)`)
	agCSRF           = regexp.MustCompile(`(?i)--csrf_token[=\s]+([^\s]+)`)
)

// antigravityServer is a running Antigravity language server: its pid and
// the CSRF token it requires.
type antigravityServer struct {
	pid  int
	csrf string
}

// antigravityServers picks the Antigravity app's and IDE's language
// servers out of the process list; a server without a CSRF token cannot
// be asked anything and is skipped.
func antigravityServers(procs []antigravityProcess) []antigravityServer {
	var out []antigravityServer
	for _, p := range procs {
		lower := strings.ToLower(p.command)
		if !agLanguageServer.MatchString(lower) || !isAntigravity(lower) {
			continue
		}
		if m := agCSRF.FindStringSubmatch(p.command); m != nil {
			out = append(out, antigravityServer{pid: p.pid, csrf: m[1]})
		}
	}
	return out
}

func isAntigravity(lower string) bool {
	return (strings.Contains(lower, "--app_data_dir") && strings.Contains(lower, "antigravity")) ||
		strings.Contains(lower, "antigravity.app/") || strings.Contains(lower, "/gemini.app/") ||
		strings.Contains(lower, "antigravity ide.app/") || strings.Contains(lower, "/antigravity/")
}

const (
	agService      = "/exa.language_server_pb.LanguageServerService/"
	agProbeTimeout = 8 * time.Second
)

func (a Antigravity) Read(ctx context.Context, _ string) (usage.Reading, error) {
	ctx, cancel := context.WithTimeout(ctx, agProbeTimeout)
	defer cancel()
	list, ports := a.Processes, a.Ports
	if list == nil {
		list = listProcesses
	}
	if ports == nil {
		ports = listeningPorts
	}
	procs, err := list(ctx)
	if err != nil {
		return usage.Reading{}, err
	}
	servers := antigravityServers(procs)
	if len(servers) == 0 {
		return usage.Reading{}, usage.ErrNotInstalled
	}
	hc := a.HTTP
	if hc == nil {
		hc = loopbackClient()
	}
	var lastErr error
	for _, s := range servers {
		pp, err := ports(ctx, s.pid)
		if err != nil {
			lastErr = err
			continue
		}
		for _, port := range pp {
			r, err := a.readServer(ctx, hc, port, s.csrf)
			if err == nil {
				return r, nil
			}
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("accounts: Antigravity is running but not listening yet")
	}
	return usage.Reading{}, lastErr
}

// readServer asks one port for the quota summary, falling back to the
// per-model quota in GetUserStatus.
func (a Antigravity) readServer(ctx context.Context, hc *http.Client, port int, csrf string) (usage.Reading, error) {
	root := "https://127.0.0.1:" + strconv.Itoa(port) + agService
	var summary json.RawMessage
	if err := agCall(ctx, hc, root+"RetrieveUserQuotaSummary", csrf, `{"forceRefresh":true}`, &summary); err == nil {
		if r, ok := parseAntigravitySummary(summary); ok {
			return r, nil
		}
	} else if errors.Is(err, usage.ErrAuth) {
		return usage.Reading{}, err
	}
	var status json.RawMessage
	meta := `{"metadata":{"ideName":"antigravity","extensionName":"antigravity","ideVersion":"unknown","locale":"en"}}`
	if err := agCall(ctx, hc, root+"GetUserStatus", csrf, meta, &status); err != nil {
		return usage.Reading{}, err
	}
	return parseAntigravityStatus(status)
}

// agCall POSTs a Connect JSON request to the local server. A "code" other
// than 0 / "ok" is an error.
func agCall(ctx context.Context, hc *http.Client, url, csrf, body string, out *json.RawMessage) error {
	b, err := send(ctx, hc, http.MethodPost, url, map[string]string{
		"Content-Type": "application/json", "Connect-Protocol-Version": "1", "X-Codeium-Csrf-Token": csrf,
	}, []byte(body))
	if err != nil {
		return err
	}
	var head struct {
		Code json.RawMessage `json:"code"`
	}
	if json.Unmarshal(b, &head) != nil {
		return fmt.Errorf("accounts: Antigravity answered in a shape this version cannot read")
	}
	if c := strings.ToLower(strings.Trim(string(head.Code), `"`)); c != "" && c != "0" && c != "ok" && c != "success" {
		return fmt.Errorf("accounts: Antigravity answered code %s", c)
	}
	*out = b
	return nil
}

// agBucket is a quota bucket in a summary group.
type agBucket struct {
	BucketID          string   `json:"bucketId"`
	ID                string   `json:"id"`
	DisplayName       string   `json:"displayName"`
	Name              string   `json:"name"`
	Disabled          bool     `json:"disabled"`
	RemainingFraction *float64 `json:"remainingFraction"`
	Remaining         *struct {
		RemainingFraction *float64 `json:"remainingFraction"`
		Case              string   `json:"case"`
		Value             *float64 `json:"value"`
	} `json:"remaining"`
	ResetTime json.RawMessage `json:"resetTime"`
	Window    string          `json:"window"`
}

func (b agBucket) fraction() (float64, bool) {
	switch {
	case b.RemainingFraction != nil:
		return *b.RemainingFraction, true
	case b.Remaining == nil:
		return 0, false
	case b.Remaining.RemainingFraction != nil:
		return *b.Remaining.RemainingFraction, true
	case b.Remaining.Case == "remainingFraction" && b.Remaining.Value != nil:
		return *b.Remaining.Value, true
	}
	return 0, false
}

// cadence is the bucket's window: 5 hours, a week, or unknown.
func (b agBucket) cadence() time.Duration {
	candidates := []string{b.Window}
	if strings.TrimSpace(b.Window) == "" {
		candidates = []string{b.BucketID, b.ID, b.DisplayName, b.Name}
	}
	for _, s := range candidates {
		s = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "_", "-")
		s = strings.TrimSuffix(s, " limit")
		for _, session := range []string{"session", "5h", "5-hour", "five hour", "five-hour"} {
			if s == session || strings.HasSuffix(s, "-"+session) {
				return 5 * time.Hour
			}
		}
		if s == "weekly" || strings.HasSuffix(s, "-weekly") {
			return 7 * 24 * time.Hour
		}
	}
	return 0
}

// agTime reads an ISO time or epoch seconds.
func agTime(raw json.RawMessage) time.Time {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if t := parseTime(s); !t.IsZero() {
			return t
		}
		raw = []byte(s)
	}
	if n, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64); err == nil && n > 0 {
		return time.Unix(int64(n), 0).UTC()
	}
	return time.Time{}
}

// agGroupTitle names a quota group as Antigravity's UI does.
func agGroupTitle(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "gemini"):
		return "Gemini"
	case strings.Contains(lower, "claude") || strings.Contains(lower, "gpt"):
		return "Claude/GPT"
	case name == "":
		return "Quota"
	}
	return name
}

// parseAntigravitySummary maps RetrieveUserQuotaSummary: per group (Gemini
// models; Claude and GPT models), a 5-hour and a weekly window. ok is
// false when no bucket carries a measured fraction.
func parseAntigravitySummary(b []byte) (usage.Reading, bool) {
	type group struct {
		DisplayName string     `json:"displayName"`
		Name        string     `json:"name"`
		Buckets     []agBucket `json:"buckets"`
	}
	type payload struct {
		Groups []group `json:"groups"`
	}
	var env struct {
		payload
		Response *payload `json:"response"`
		Summary  *payload `json:"summary"`
	}
	if json.Unmarshal(b, &env) != nil {
		return usage.Reading{}, false
	}
	p := env.payload
	if env.Response != nil {
		p = *env.Response
	} else if env.Summary != nil {
		p = *env.Summary
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	for _, g := range p.Groups {
		title := g.DisplayName
		if title == "" {
			title = g.Name
		}
		title = agGroupTitle(title)
		for _, bk := range g.Buckets {
			f, ok := bk.fraction()
			if bk.Disabled || !ok || (bk.BucketID == "" && bk.ID == "") {
				continue
			}
			d := bk.cadence()
			r.Windows = append(r.Windows, usage.Window{
				Name: windowName(d) + " (" + title + ")", UsedPct: clampPct(100 - f*100), Duration: d, ResetsAt: agTime(bk.ResetTime),
			})
		}
	}
	sort.SliceStable(r.Windows, func(i, j int) bool { return r.Windows[i].Duration < r.Windows[j].Duration })
	return r, len(r.Windows) > 0
}

// parseAntigravityStatus maps GetUserStatus, which older apps answer
// instead: per model pool, the most used model's quota. Its window length
// is not reported.
func parseAntigravityStatus(b []byte) (usage.Reading, error) {
	var resp struct {
		UserStatus *struct {
			CascadeModelConfigData *struct {
				ClientModelConfigs []struct {
					Label        string `json:"label"`
					ModelOrAlias struct {
						Model string `json:"model"`
					} `json:"modelOrAlias"`
					QuotaInfo *struct {
						RemainingFraction *float64        `json:"remainingFraction"`
						ResetTime         json.RawMessage `json:"resetTime"`
					} `json:"quotaInfo"`
				} `json:"clientModelConfigs"`
			} `json:"cascadeModelConfigData"`
		} `json:"userStatus"`
	}
	if json.Unmarshal(b, &resp) != nil || resp.UserStatus == nil {
		return usage.Reading{}, fmt.Errorf("accounts: Antigravity user status in a shape this version cannot read")
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	if resp.UserStatus.CascadeModelConfigData == nil {
		return r, nil
	}
	pools := map[string]usage.Window{}
	var order []string
	for _, m := range resp.UserStatus.CascadeModelConfigData.ClientModelConfigs {
		if m.QuotaInfo == nil || m.QuotaInfo.RemainingFraction == nil {
			continue
		}
		id := strings.ToLower(m.ModelOrAlias.Model + " " + m.Label)
		if strings.Contains(id, "lite") || strings.Contains(id, "autocomplete") || strings.HasPrefix(id, "tab_") || strings.Contains(id, "image") {
			continue
		}
		pool := ""
		switch {
		case strings.Contains(id, "claude") || strings.Contains(id, "gpt") || strings.Contains(id, "openai"):
			pool = "Claude/GPT"
		case strings.Contains(id, "gemini"):
			pool = "Gemini"
		default:
			continue
		}
		w := usage.Window{Name: "quota (" + pool + ")", UsedPct: clampPct(100 - *m.QuotaInfo.RemainingFraction*100), ResetsAt: agTime(m.QuotaInfo.ResetTime)}
		prev, seen := pools[pool]
		if !seen {
			order = append(order, pool)
		}
		if !seen || w.UsedPct > prev.UsedPct {
			pools[pool] = w
		}
	}
	sort.Strings(order)
	for _, p := range order {
		r.Windows = append(r.Windows, pools[p])
	}
	return r, nil
}

// loopbackClient talks only to 127.0.0.1, whose language server presents
// a self-signed certificate: it never dials anything else, so skipping
// verification cannot send the token off the machine.
func loopbackClient() *http.Client {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	return &http.Client{
		Timeout: agProbeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil || host != "127.0.0.1" {
					return nil, fmt.Errorf("accounts: Antigravity is only asked on 127.0.0.1")
				}
				return dialer.DialContext(ctx, network, addr)
			},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // loopback only: the dialer refuses any other host
			Proxy:           nil,
		},
	}
}

// listProcesses is `ps -ax -o pid=,command=`.
func listProcesses(ctx context.Context) ([]antigravityProcess, error) {
	out, err := runCLI(ctx, 5*time.Second, "/bin/ps", nil, "-ax", "-o", "pid=,command=")
	if err != nil {
		return nil, err
	}
	var procs []antigravityProcess
	for _, line := range strings.Split(out, "\n") {
		pid, cmd, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(pid); err == nil {
			procs = append(procs, antigravityProcess{pid: n, command: strings.TrimSpace(cmd)})
		}
	}
	return procs, nil
}

var lsofListen = regexp.MustCompile(`:(\d+)\s+\(LISTEN\)`)

// listeningPorts is the TCP ports pid listens on, from lsof.
func listeningPorts(ctx context.Context, pid int) ([]int, error) {
	bin := "/usr/sbin/lsof"
	if !isExecutable(bin) {
		bin = "/usr/bin/lsof"
	}
	out, err := runCLI(ctx, 5*time.Second, bin, nil, "-nP", "-iTCP", "-sTCP:LISTEN", "-a", "-p", strconv.Itoa(pid))
	// lsof exits 1 with no output when pid listens on nothing.
	if err != nil && (!errors.Is(err, errCLIFailed) || strings.TrimSpace(out) != "") {
		return nil, err
	}
	seen := map[int]bool{}
	var ports []int
	for _, m := range lsofListen.FindAllStringSubmatch(out, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && !seen[n] {
			seen[n] = true
			ports = append(ports, n)
		}
	}
	sort.Ints(ports)
	return ports, nil
}
