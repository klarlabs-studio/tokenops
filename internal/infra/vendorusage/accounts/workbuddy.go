package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerWorkBuddy registers the reader (readers_gen.go).
func readerWorkBuddy() usage.Reader { return WorkBuddy{} }

// WorkBuddy reads the credits left in Tencent WorkBuddy's credit packages
// with the www.workbuddy.cn browser session, as CodexBar's WorkBuddy
// provider does (Sources/CodexBarCore/Resources/Plugins/workbuddy.ts,
// Providers/WorkBuddy/WorkBuddyChromeVersion.swift, docs/workbuddy.md):
// the resource summary sums every credit package's cycle; the package
// listings give the cycle's end, in China Standard Time. WorkBuddy binds
// the session to the browser's User-Agent, so the request names the
// installed Chrome's major version, and the one before it when Chrome has
// updated but not restarted.
type WorkBuddy struct {
	BaseURL string
	HTTP    *http.Client
	Now     func() time.Time
	// Chrome is the installed Chrome's major version; 0 finds it.
	Chrome int
}

func (WorkBuddy) Endpoint() string               { return "workbuddy" }
func (WorkBuddy) Provider() eventschema.Provider { return "workbuddy" }
func (WorkBuddy) Source() string                 { return "workbuddy-web" }

// The package codes the listings require, as WorkBuddy's page sends them.
var (
	workBuddyPaid = []string{"TCACA_code_002_AkiJS3ZHF5", "TCACA_code_005_maRGyrHhw1", "TCACA_code_003_FAnt7lcmRT",
		"TCACA_code_023_4xbGhMrE6q", "TCACA_code_026_BaESVICNoi", "TCACA_code_027_0FCGVA6vSa", "TCACA_code_009_0XmEQc2xOf",
		"TCACA_code_038_OhvqZtiPKr", "TCACA_code_036_lupO5WgNdG"}
	workBuddyFree = []string{"TCACA_code_001_PqouKr6QWV", "TCACA_code_008_cfWoLwvjU4", "TCACA_code_035_ArVxJcGDsm",
		"TCACA_code_006_DbXS0lrypC", "TCACA_code_039_KRcQj7wUat", "TCACA_code_040_mi9rCYg46x", "TCACA_code_007_nzdH5h4Nl0",
		"TCACA_code_028_NtpWi0jzXs", "TCACA_code_037_WxOD3MpI2o", "TCACA_code_029_6wCGEWquYy", "TCACA_code_030_BjSt89qTvr"}
	workBuddyAmount = regexp.MustCompile(`^\d+(?:\.\d+)?$`)
	workBuddyEnd    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)
	workBuddyCST    = time.FixedZone("CST", 8*3600)
)

func (wb WorkBuddy) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	cookie = cookieHeader(cookie)
	if !isSession(cookie) {
		return usage.Reading{}, fmt.Errorf("%w (WorkBuddy is read with www.workbuddy.cn's Cookie header)", usage.ErrAuth)
	}
	root := base(wb.BaseURL, "https://www.workbuddy.cn")
	chrome := wb.Chrome
	if chrome == 0 {
		chrome = installedChromeMajor()
	}
	agents := []string{""}
	if chrome > 1 {
		agents = []string{chromeUA(chrome), chromeUA(chrome - 1)}
	}
	var body []byte
	var err error
	agent := ""
	for _, agent = range agents {
		body, err = wb.post(ctx, root+"/billing/meter/get-user-resource-summary", agent, cookie, map[string]any{})
		if !errors.Is(err, usage.ErrAuth) {
			break
		}
	}
	if err != nil {
		return usage.Reading{}, err
	}
	var resp struct {
		Code *int `json:"code"`
		Data *struct {
			Packages *[]struct {
				Unit   string          `json:"CapacityUnit"`
				Total  json.RawMessage `json:"CycleTotalCapacity"`
				Remain json.RawMessage `json:"CycleRemainCapacity"`
			} `json:"Packages"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &resp) != nil || resp.Code == nil {
		return usage.Reading{}, errors.New("accounts: POST www.workbuddy.cn summary: unexpected shape")
	}
	if *resp.Code != 0 {
		return usage.Reading{}, fmt.Errorf("accounts: POST www.workbuddy.cn summary: code %d", *resp.Code)
	}
	if resp.Data == nil || resp.Data.Packages == nil {
		return usage.Reading{}, errors.New("accounts: POST www.workbuddy.cn summary: no packages")
	}
	total, remain := 0.0, 0.0
	for _, p := range *resp.Data.Packages {
		if p.Unit != "credits" {
			continue
		}
		t, ok1 := workBuddyNumber(p.Total)
		left, ok2 := workBuddyNumber(p.Remain)
		if !ok1 || !ok2 {
			return usage.Reading{}, errors.New("accounts: POST www.workbuddy.cn summary: unreadable amount")
		}
		total, remain = total+t, remain+left
	}
	r := usage.Reading{Scope: "account"}
	if total <= 0 {
		return r, nil
	}
	r.Subscription = true
	r.Windows = []usage.Window{{Name: "credits", UsedPct: clampPct(pct(max(0, total-remain), total)), ResetsAt: wb.cycleEnd(ctx, root, agent, cookie)}}
	return r, nil
}

// cycleEnd is the soonest future end of a credit package's cycle, from
// the paid and free listings; a listing that fails is skipped.
func (wb WorkBuddy) cycleEnd(ctx context.Context, root, agent, cookie string) time.Time {
	now := time.Now
	if wb.Now != nil {
		now = wb.Now
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var soonest time.Time
	for _, l := range []struct {
		path  string
		codes []string
	}{{"get-user-resource-paid-packages", workBuddyPaid}, {"get-user-resource-free-packages", workBuddyFree}} {
		body, err := wb.post(ctx, root+"/billing/meter/"+l.path, agent, cookie,
			map[string]any{"PageNumber": 1, "PageSize": 100, "PackageCodes": l.codes, "Status": []int{0, 3}})
		if err != nil {
			continue
		}
		var resp struct {
			Code int `json:"code"`
			Data struct {
				Accounts []struct {
					Unit string `json:"CapacityUnit"`
					End  string `json:"CycleEndTime"`
				} `json:"Accounts"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &resp) != nil || resp.Code != 0 {
			continue
		}
		for _, a := range resp.Data.Accounts {
			if a.Unit != "credits" || !workBuddyEnd.MatchString(a.End) {
				continue
			}
			end, err := time.ParseInLocation("2006-01-02 15:04:05", a.End, workBuddyCST)
			if err != nil {
				continue
			}
			end = end.Add(time.Second).UTC()
			if end.After(now()) && (soonest.IsZero() || end.Before(soonest)) {
				soonest = end
			}
		}
	}
	return soonest
}

func (wb WorkBuddy) post(ctx context.Context, u, agent, cookie string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	header := http.Header{
		"Cookie": {cookie}, "Accept": {"application/json"}, "Content-Type": {"application/json"},
		"Origin": {"https://www.workbuddy.cn"}, "Referer": {"https://www.workbuddy.cn/profile/plans-usage"},
	}
	if agent != "" {
		header.Set("User-Agent", agent)
	}
	return doWeb(ctx, wb.HTTP, http.MethodPost, u, header, body)
}

// workBuddyNumber is a non-negative decimal string or number.
func workBuddyNumber(raw json.RawMessage) (float64, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if !workBuddyAmount.MatchString(s) {
			return 0, false
		}
		v, err := strconv.ParseFloat(s, 64)
		return v, err == nil
	}
	v, ok := finiteNumber(raw)
	return v, ok && v >= 0 && !math.IsInf(v, 0)
}

func chromeUA(major int) string {
	return "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" +
		strconv.Itoa(major) + ".0.0.0 Safari/537.36"
}

var chromeVersion = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>(\d+)\.`)

// installedChromeMajor is Google Chrome's major version from its
// Info.plist (no secret), 0 when Chrome is not installed there.
func installedChromeMajor() int {
	home, _ := os.UserHomeDir()
	for _, p := range []string{"/Applications/Google Chrome.app/Contents/Info.plist",
		filepath.Join(home, "Applications/Google Chrome.app/Contents/Info.plist")} {
		b, err := os.ReadFile(p) // #nosec G304 -- a fixed application path
		if err != nil {
			continue
		}
		if m := chromeVersion.FindSubmatch(b); m != nil {
			if v, err := strconv.Atoi(string(m[1])); err == nil && v > 1 {
				return v
			}
		}
	}
	return 0
}
