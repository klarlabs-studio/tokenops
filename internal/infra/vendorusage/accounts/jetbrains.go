package accounts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerJetBrains registers the reader (readers_gen.go).
func readerJetBrains() usage.Reader { return JetBrains{} }

// JetBrains reads JetBrains AI's monthly credits from the file the IDE's
// AI Assistant keeps, options/AIAssistantQuotaManager2.xml, in the most
// recently used IDE, and from that IDE's idea.log when the log holds a
// newer quota state (the IDE logs every refresh but rewrites the XML
// rarely). Nothing leaves the machine and no secret is read. The file is
// internal to the IDE and its format undocumented; this follows CodexBar.
type JetBrains struct {
	// Home overrides the home directory and GOOS the system, for tests.
	Home string
	GOOS string
}

func (JetBrains) Endpoint() string               { return "jetbrains" }
func (JetBrains) Provider() eventschema.Provider { return "jetbrains" }
func (JetBrains) Source() string                 { return "jetbrains-local" }
func (JetBrains) Keyless()                       {}

// jetbrainsIDEs are the IDE configuration directory prefixes.
var jetbrainsIDEs = []string{
	"IntelliJIdea", "PyCharm", "WebStorm", "GoLand", "CLion", "DataGrip", "RubyMine", "Rider",
	"PhpStorm", "AppCode", "Fleet", "AndroidStudio", "RustRover", "Aqua", "DataSpell",
}

const jetbrainsQuotaFile = "AIAssistantQuotaManager2.xml"

// jetbrainsIDE is one IDE installation with a quota file.
type jetbrainsIDE struct {
	vendor, dir, quota string
	modified           time.Time
}

func (j JetBrains) home() string {
	if j.Home != "" {
		return j.Home
	}
	h, _ := os.UserHomeDir()
	return h
}

func (j JetBrains) goos() string {
	if j.GOOS != "" {
		return j.GOOS
	}
	return runtime.GOOS
}

// bases are the directories holding IDE configuration directories, by
// vendor ("JetBrains", or "Google" for Android Studio).
func (j JetBrains) bases() [][2]string {
	h := j.home()
	if j.goos() == "darwin" {
		return [][2]string{
			{"JetBrains", filepath.Join(h, "Library", "Application Support", "JetBrains")},
			{"Google", filepath.Join(h, "Library", "Application Support", "Google")},
		}
	}
	return [][2]string{
		{"JetBrains", filepath.Join(h, ".config", "JetBrains")},
		{"JetBrains", filepath.Join(h, ".local", "share", "JetBrains")},
		{"Google", filepath.Join(h, ".config", "Google")},
	}
}

// latestIDE is the IDE whose quota file was written last.
func (j JetBrains) latestIDE() (jetbrainsIDE, bool) {
	var best jetbrainsIDE
	found := false
	for _, b := range j.bases() {
		entries, err := os.ReadDir(b[1])
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !isJetBrainsIDE(e.Name()) {
				continue
			}
			p := filepath.Join(b[1], e.Name(), "options", jetbrainsQuotaFile)
			info, err := os.Stat(p)
			if err != nil || info.IsDir() {
				continue
			}
			if !found || info.ModTime().After(best.modified) {
				best, found = jetbrainsIDE{vendor: b[0], dir: e.Name(), quota: p, modified: info.ModTime()}, true
			}
		}
	}
	return best, found
}

func isJetBrainsIDE(dir string) bool {
	lower := strings.ToLower(dir)
	for _, p := range jetbrainsIDEs {
		if strings.HasPrefix(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// jetbrainsQuota is the monthly credit state: used of max, refilled at
// next.
type jetbrainsQuota struct {
	used, max float64
	next      time.Time
	period    time.Duration
}

func (j JetBrains) Read(_ context.Context, _ string) (usage.Reading, error) {
	ide, ok := j.latestIDE()
	if !ok {
		return usage.Reading{}, usage.ErrNotInstalled
	}
	b, err := os.ReadFile(ide.quota) //nolint:gosec // the IDE's own quota file under the operator's home
	if err != nil {
		return usage.Reading{}, fmt.Errorf("accounts: read JetBrains quota: %w", err)
	}
	q, err := parseJetBrainsXML(b)
	if err != nil {
		return usage.Reading{}, err
	}
	if logged, at, ok := j.loggedQuota(ide); ok && at.After(ide.modified) {
		q = logged
	}
	if q.max <= 0 {
		return usage.Reading{Scope: "account", Subscription: true}, nil
	}
	period := q.period
	if period <= 0 {
		period = 30 * 24 * time.Hour
	}
	return usage.Reading{Scope: "account", Subscription: true, Windows: []usage.Window{{
		Name: windowName(period), UsedPct: math.Max(0, math.Min(pct(q.used, q.max), 100)), Duration: period, ResetsAt: q.next,
	}}}, nil
}

// errNoQuotaInfo is a quota file without the quota: AI Assistant has not
// fetched one yet, or the format changed.
var errNoQuotaInfo = errors.New("accounts: the JetBrains quota file holds no quota")

// parseJetBrainsXML reads quotaInfo and nextRefill from the quota file.
// Their values are JSON, every number a string.
func parseJetBrainsXML(b []byte) (jetbrainsQuota, error) {
	var doc struct {
		Components []struct {
			Name    string `xml:"name,attr"`
			Options []struct {
				Name  string `xml:"name,attr"`
				Value string `xml:"value,attr"`
			} `xml:"option"`
		} `xml:"component"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		return jetbrainsQuota{}, fmt.Errorf("accounts: JetBrains quota file: %w", err)
	}
	var info, refill string
	for _, c := range doc.Components {
		if c.Name != "AIAssistantQuotaManager2" {
			continue
		}
		for _, o := range c.Options {
			switch o.Name {
			case "quotaInfo":
				info = o.Value
			case "nextRefill":
				refill = o.Value
			}
		}
	}
	if strings.TrimSpace(info) == "" {
		return jetbrainsQuota{}, errNoQuotaInfo
	}
	type amounts struct {
		Current   *string `json:"current"`
		Maximum   *string `json:"maximum"`
		Available *string `json:"available"`
	}
	var qi struct {
		amounts
		TariffQuota *amounts `json:"tariffQuota"`
	}
	if err := json.Unmarshal([]byte(info), &qi); err != nil {
		return jetbrainsQuota{}, fmt.Errorf("accounts: JetBrains quotaInfo in a shape this version cannot read")
	}
	var q jetbrainsQuota
	// The monthly tariff is what the IDE shows as "monthly credits left";
	// top-ups only inflate the total. Monthly and total figures are never
	// mixed.
	if t := qi.TariffQuota; t != nil && finite(t.Current) && finite(t.Maximum) {
		q.used, q.max = num(t.Current), num(t.Maximum)
	} else {
		q.used, q.max = num(qi.Current), num(qi.Maximum)
	}
	var nr struct {
		Type     string  `json:"type"`
		Next     string  `json:"next"`
		Duration *string `json:"duration"`
		Tariff   *struct {
			Duration *string `json:"duration"`
		} `json:"tariff"`
	}
	if refill != "" && json.Unmarshal([]byte(refill), &nr) == nil {
		q.next = parseTime(nr.Next)
		d := nr.Duration
		if d == nil && nr.Tariff != nil {
			d = nr.Tariff.Duration
		}
		if d != nil {
			q.period = isoDuration(*d)
		}
	}
	return q, nil
}

func finite(s *string) bool {
	if s == nil {
		return false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(*s), 64)
	return err == nil && !math.IsInf(v, 0) && !math.IsNaN(v)
}

func num(s *string) float64 {
	if !finite(s) {
		return 0
	}
	v, _ := strconv.ParseFloat(strings.TrimSpace(*s), 64)
	return v
}

// isoDuration reads the hour and day forms JetBrains writes: "PT720H",
// "P30D", and the log's "30d".
func isoDuration(s string) time.Duration {
	s = strings.ToUpper(strings.TrimSpace(s))
	for _, f := range []struct {
		prefix, suffix string
		unit           time.Duration
	}{{"PT", "H", time.Hour}, {"P", "D", 24 * time.Hour}, {"", "D", 24 * time.Hour}} {
		if strings.HasPrefix(s, f.prefix) && strings.HasSuffix(s, f.suffix) {
			if n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(s, f.prefix), f.suffix)); err == nil && n > 0 {
				return time.Duration(n) * f.unit
			}
		}
	}
	return 0
}

// jetbrainsLogTail is how much of idea.log is read, from its end.
const jetbrainsLogTail = 4 << 20

var (
	jetbrainsLogState  = regexp.MustCompile(`QuotaManager2Impl - New quota state is: Available\(current=[0-9.]+, maximum=[0-9.]+, until=[^,]*, tariffQuota=QuotaDetails\(current=([0-9.]+), maximum=([0-9.]+), available=[0-9.]+\)`)
	jetbrainsLogRefill = regexp.MustCompile(`QuotaManager2Impl - New quota refill state is: Known\(next=([^,]+), tariff=QuotaRefillInfoTariff\(amount=[0-9.]+, duration=([^)]+)\)\)`)
)

// loggedQuota is the newest quota state ide's own idea.log records, and
// when it was logged. Only that installation's log is read: another IDE
// may be signed in to another account. A newest state that does not parse
// is not replaced with an older one.
func (j JetBrains) loggedQuota(ide jetbrainsIDE) (jetbrainsQuota, time.Time, bool) {
	p := filepath.Join(j.home(), "Library", "Logs", ide.vendor, ide.dir, "idea.log")
	if j.goos() != "darwin" {
		p = filepath.Join(j.home(), ".cache", ide.vendor, ide.dir, "log", "idea.log")
	}
	lines, ok := tailLines(p, jetbrainsLogTail)
	if !ok {
		return jetbrainsQuota{}, time.Time{}, false
	}
	var q jetbrainsQuota
	var at time.Time
	haveState, haveRefill := false, false
	for i := len(lines) - 1; i >= 0 && (!haveState || !haveRefill); i-- {
		line := lines[i]
		switch {
		case !haveState && strings.Contains(line, "New quota state is:"):
			haveState = true
			m := jetbrainsLogState.FindStringSubmatch(line)
			if m == nil {
				return jetbrainsQuota{}, time.Time{}, false
			}
			q.used, _ = strconv.ParseFloat(m[1], 64)
			q.max, _ = strconv.ParseFloat(m[2], 64)
			if at = logTime(line); at.IsZero() || q.max <= 0 {
				return jetbrainsQuota{}, time.Time{}, false
			}
		case !haveRefill && strings.Contains(line, "New quota refill state is:"):
			haveRefill = true
			if m := jetbrainsLogRefill.FindStringSubmatch(line); m != nil {
				q.next, q.period = parseTime(m[1]), isoDuration(m[2])
			}
		}
	}
	return q, at, haveState
}

// logTime is an idea.log line's local timestamp.
func logTime(line string) time.Time {
	if len(line) < 23 {
		return time.Time{}
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05,000", line[:23], time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

// tailLines reads at most limit bytes from the end of the file at p, as of
// its size when opened, dropping a partial first line. An unfinished last
// line rejects the tail: the IDE is writing it.
func tailLines(p string, limit int64) ([]string, bool) {
	f, err := os.Open(p) //nolint:gosec // the IDE's own log under the operator's home
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return nil, false
	}
	start := max(0, info.Size()-limit)
	buf := make([]byte, info.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, false
	}
	if buf[len(buf)-1] != '\n' {
		return nil, false
	}
	if start > 0 {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(buf))
	sc.Buffer(make([]byte, 64*1024), len(buf)+1)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err() == nil
}
