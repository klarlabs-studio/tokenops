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

// jetbrainsQuota is the credit state: used of max on the monthly tariff,
// refilled at next, and purchased top-up credits left when reported.
type jetbrainsQuota struct {
	used, max float64
	next      time.Time
	period    time.Duration
	topUp     float64
	hasTopUp  bool
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
	var notice string
	logged := j.loggedQuota(ide)
	switch {
	case logged.ok && logged.at.After(ide.modified):
		q = logged.quota
		if logged.problem != "" {
			notice = "using quota from idea.log; " + logged.problem
		}
	case logged.problem != "":
		notice = fmt.Sprintf("using stale quota from %s, last written %s; %s",
			ide.quota, ide.modified.Format("2006-01-02"), logged.problem)
	}
	r := jetbrainsReading(q)
	r.Notice = notice
	return r, nil
}

// jetbrainsReading is the account reading for a quota. A quota with no
// maximum is an account with nothing to measure, not an error.
func jetbrainsReading(q jetbrainsQuota) usage.Reading {
	r := usage.Reading{Scope: "account", Subscription: true}
	if q.hasTopUp {
		r.Credits, r.CreditsUnit, r.HasCredits = q.topUp, "credits", true
	}
	if q.max <= 0 {
		return r
	}
	period := q.period
	if period <= 0 {
		period = 30 * 24 * time.Hour
	}
	r.Windows = []usage.Window{{
		Name: windowName(period), UsedPct: math.Max(0, math.Min(pct(q.used, q.max), 100)), Duration: period, ResetsAt: q.next,
	}}
	return r
}

// errNoQuotaInfo is a quota file without the quota: AI Assistant has not
// fetched one yet, or the format changed.
var errNoQuotaInfo = errors.New("accounts: the JetBrains quota file holds no quota")

// errQuotaShape is a quota whose figures this version cannot find.
var errQuotaShape = errors.New("accounts: JetBrains quotaInfo holds no quota figures in a shape this version reads")

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
		TopUpQuota  *amounts `json:"topUpQuota"`
	}
	if err := json.Unmarshal([]byte(info), &qi); err != nil {
		return jetbrainsQuota{}, fmt.Errorf("accounts: JetBrains quotaInfo in a shape this version cannot read")
	}
	var q jetbrainsQuota
	// The monthly tariff is what the IDE shows as "monthly credits left";
	// top-ups only inflate the total. Monthly and total figures are never
	// mixed.
	switch t := qi.TariffQuota; {
	case t != nil && finite(t.Current) && finite(t.Maximum):
		q.used, q.max = num(t.Current), num(t.Maximum)
	case finite(qi.Current) && finite(qi.Maximum):
		q.used, q.max = num(qi.Current), num(qi.Maximum)
	default:
		return jetbrainsQuota{}, errQuotaShape
	}
	if t := qi.TopUpQuota; t != nil && finite(t.Available) {
		q.topUp, q.hasTopUp = num(t.Available), true
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

const (
	jetbrainsQuotaMarker   = "QuotaManager2Impl - New quota state is: "
	jetbrainsRefillMarker  = "QuotaManager2Impl - New quota refill state is: "
	jetbrainsJunieMarker   = "AiaActivationTransmitter - Received message from junie: quota_update:"
	jetbrainsDisabledState = "Error(exception=com.intellij.ml.llm.core.AIAssistantForbiddenException"
)

// Numbers are matched but only the amounts are captured; license, quota and
// user identifiers are matched by shape and never kept.
const jbaiNumber = `([0-9.]+)`

var (
	// The layout before AI Assistant 262.x.
	jetbrainsLegacyState = lazyRegexp(`Available\(current=[0-9.]+, maximum=[0-9.]+, until=[^,]*, ` +
		`tariffQuota=QuotaDetails\(current=` + jbaiNumber + `, maximum=` + jbaiNumber + `, available=[0-9.]+\)` +
		`(?:, topUpQuota=QuotaDetails\(current=[0-9.]+, maximum=[0-9.]+, available=` + jbaiNumber + `\))?`)
	// The JbaiQuota layout AI Assistant 262.x logs.
	jetbrainsJbaiState = lazyRegexp(`Available\(quota=JbaiQuota\(license=[^,()]*, current=[0-9.]+, maximum=[0-9.]+, ` +
		`quotaID=JbaiQuotaId\([^()]*\), until=[^,]*, ` +
		`tariffQuota=Details\(current=` + jbaiNumber + `, maximum=` + jbaiNumber + `, available=[0-9.]+\)` +
		`(?:, topUpQuota=Details\(current=[0-9.]+, maximum=[0-9.]+, available=` + jbaiNumber + `\))?`)
	jetbrainsLegacyRefill = lazyRegexp(`Known\(next=([^,]+), tariff=QuotaRefillInfoTariff\(amount=[0-9.]+, duration=([^)]+)\)\)`)
	jetbrainsJbaiRefill   = lazyRegexp(`Known\(refill=JbaiQuotaRefill\(tariff=JbaiQuotaTariff\(amount=[0-9.]+, period=([^,)]+)\), ` +
		`next=([^,]+), last=[^)]*\)\)`)
)

// jetbrainsLogQuota is the newest quota state idea.log holds for this
// installation. ok is set when that state was read; problem says what
// could not be, so health can name it.
type jetbrainsLogQuota struct {
	quota   jetbrainsQuota
	at      time.Time
	ok      bool
	problem string
}

// loggedQuota reads the newest quota record of ide's own idea.log. Only that
// installation's log is read: another IDE may be signed in to another
// account. A newest record in an unrecognised layout is not replaced with an
// older one; the caller falls back to the quota file and says so.
func (j JetBrains) loggedQuota(ide jetbrainsIDE) jetbrainsLogQuota {
	p := filepath.Join(j.home(), "Library", "Logs", ide.vendor, ide.dir, "idea.log")
	if j.goos() != "darwin" {
		p = filepath.Join(j.home(), ".cache", ide.vendor, ide.dir, "log", "idea.log")
	}
	lines, ok := tailLines(p, jetbrainsLogTail)
	if !ok {
		return jetbrainsLogQuota{}
	}
	var out jetbrainsLogQuota
	haveQuota, haveRefill := false, false
	for i := len(lines) - 1; i >= 0 && (!haveQuota || !haveRefill); i-- {
		line := lines[i]
		switch {
		case !haveQuota && isLogQuotaLine(line):
			haveQuota = true
			q, at, parsed := parseLogQuota(line)
			if !parsed {
				return jetbrainsLogQuota{problem: "the newest quota record in idea.log is in an unrecognised layout"}
			}
			// The refill is read from its own line, newer or older than the
			// state; keep it when the state replaces the quota.
			q.next, q.period = out.quota.next, out.quota.period
			out.quota, out.at, out.ok = q, at, true
		case !haveRefill && strings.Contains(line, jetbrainsRefillMarker):
			haveRefill = true
			if next, period, parsed := parseLogRefill(line); parsed {
				out.quota.next, out.quota.period = next, period
			} else {
				out.problem = "the newest refill record in idea.log is in an unrecognised layout, so the reset time is not shown"
			}
		}
	}
	return out
}

// isLogQuotaLine is a line carrying the account's quota. The disabled
// assistant error says nothing about the account, so it is skipped: Junie's
// record before it still holds.
func isLogQuotaLine(line string) bool {
	if strings.Contains(line, jetbrainsJunieMarker) {
		return true
	}
	return strings.Contains(line, jetbrainsQuotaMarker) && !strings.Contains(line, jetbrainsDisabledState)
}

// parseLogQuota reads one quota record and when it was logged. A record
// with no maximum is not a quota this reader can show.
func parseLogQuota(line string) (jetbrainsQuota, time.Time, bool) {
	at := logTime(line)
	if at.IsZero() {
		return jetbrainsQuota{}, time.Time{}, false
	}
	var q jetbrainsQuota
	var ok bool
	if i := strings.Index(line, jetbrainsJunieMarker); i >= 0 {
		q, ok = parseJunieQuota(line[i+len(jetbrainsJunieMarker):])
	} else {
		q, ok = parseLogState(line)
	}
	return q, at, ok && q.max > 0
}

// parseLogState reads an Available state in either the legacy or the
// JbaiQuota layout.
func parseLogState(line string) (jetbrainsQuota, bool) {
	m := jetbrainsLegacyState().FindStringSubmatch(line)
	if m == nil {
		m = jetbrainsJbaiState().FindStringSubmatch(line)
	}
	if m == nil {
		return jetbrainsQuota{}, false
	}
	used, okU := parseAmount(m[1])
	maxi, okM := parseAmount(m[2])
	if !okU || !okM {
		return jetbrainsQuota{}, false
	}
	q := jetbrainsQuota{used: used, max: maxi}
	if m[3] != "" {
		topUp, okT := parseAmount(m[3])
		if !okT {
			return jetbrainsQuota{}, false
		}
		q.topUp, q.hasTopUp = topUp, true
	}
	return q, true
}

// parseLogRefill reads a refill record in either layout: next refill and
// the period it refills over.
func parseLogRefill(line string) (time.Time, time.Duration, bool) {
	var next, period string
	if m := jetbrainsLegacyRefill().FindStringSubmatch(line); m != nil {
		next, period = m[1], m[2]
	} else if m := jetbrainsJbaiRefill().FindStringSubmatch(line); m != nil {
		period, next = m[1], m[2]
	} else {
		return time.Time{}, 0, false
	}
	t, d := parseTime(next), isoDuration(period)
	return t, d, !t.IsZero() && d > 0
}

// junieAmount is one of Junie's amounts, {"amount": "12.5"} in the log.
type junieAmount struct {
	Amount json.Number `json:"amount"`
}

// junieDetails is a quota split the way Junie writes it.
type junieDetails struct {
	Current   *junieAmount `json:"current"`
	Maximum   *junieAmount `json:"maximum"`
	Available *junieAmount `json:"available"`
}

// parseJunieQuota reads the JSON of a Junie quota_update record. Only the
// quota amounts are read: the license and expiry are never kept.
func parseJunieQuota(payload string) (jetbrainsQuota, bool) {
	var u struct {
		TariffQuota *junieDetails `json:"tariffQuota"`
		TopUpQuota  *junieDetails `json:"topUpQuota"`
	}
	if err := json.NewDecoder(strings.NewReader(payload)).Decode(&u); err != nil || u.TariffQuota == nil {
		return jetbrainsQuota{}, false
	}
	t := u.TariffQuota
	used, okU := junieValue(t.Current)
	maxi, okM := junieValue(t.Maximum)
	_, okA := junieValue(t.Available)
	if !okU || !okM || !okA {
		return jetbrainsQuota{}, false
	}
	q := jetbrainsQuota{used: used, max: maxi}
	if u.TopUpQuota != nil {
		if topUp, ok := junieValue(u.TopUpQuota.Available); ok {
			q.topUp, q.hasTopUp = topUp, true
		}
	}
	return q, true
}

func junieValue(a *junieAmount) (float64, bool) {
	if a == nil {
		return 0, false
	}
	return parseAmount(string(a.Amount))
}

// parseAmount reads a finite, non-negative amount.
func parseAmount(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) || v < 0 {
		return 0, false
	}
	return v, true
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
