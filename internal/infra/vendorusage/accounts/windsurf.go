package accounts

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"

	// Registers the sqlite driver used to read Windsurf's state store.
	_ "modernc.org/sqlite"
)

// Windsurf is read two ways, after CodexBar: the plan Windsurf caches in
// its own state store (stale while Windsurf is closed, but local and
// keyless), and windsurf.com's GetPlanStatus with the web session a
// browser holds, typed into `tokenops vendor-usage setup windsurf`.

// readerWindsurfLocal registers the local reader (readers_gen.go).
func readerWindsurfLocal() usage.Reader { return WindsurfLocal{} }

// WindsurfLocal reads windsurf.settings.cachedPlanInfo from Windsurf's
// state.vscdb, opened read-only. Windsurf rewrites it when it runs.
type WindsurfLocal struct {
	// Path overrides the state store's location, for tests.
	Path string
}

func (WindsurfLocal) Endpoint() string               { return "windsurf" }
func (WindsurfLocal) Provider() eventschema.Provider { return "windsurf" }
func (WindsurfLocal) Source() string                 { return "windsurf-local" }
func (WindsurfLocal) Keyless()                       {}

func (l WindsurfLocal) path() string {
	if l.Path != "" {
		return l.Path
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Windsurf", "User", "globalStorage", "state.vscdb")
	}
	return filepath.Join(home, ".config", "Windsurf", "User", "globalStorage", "state.vscdb")
}

// windsurfCachedPlan is the cached plan's JSON. Timestamps are
// milliseconds, except the quota resets, which are seconds.
type windsurfCachedPlan struct {
	PlanName     string `json:"planName"`
	EndTimestamp int64  `json:"endTimestamp"`
	Usage        *struct {
		Messages             *int64 `json:"messages"`
		UsedMessages         *int64 `json:"usedMessages"`
		RemainingMessages    *int64 `json:"remainingMessages"`
		FlowActions          *int64 `json:"flowActions"`
		UsedFlowActions      *int64 `json:"usedFlowActions"`
		RemainingFlowActions *int64 `json:"remainingFlowActions"`
	} `json:"usage"`
	QuotaUsage *struct {
		DailyRemainingPercent  *float64 `json:"dailyRemainingPercent"`
		WeeklyRemainingPercent *float64 `json:"weeklyRemainingPercent"`
		DailyResetAtUnix       int64    `json:"dailyResetAtUnix"`
		WeeklyResetAtUnix      int64    `json:"weeklyResetAtUnix"`
	} `json:"quotaUsage"`
}

func (l WindsurfLocal) Read(ctx context.Context, _ string) (usage.Reading, error) {
	p := l.path()
	if _, err := os.Stat(p); err != nil {
		return usage.Reading{}, usage.ErrNotInstalled
	}
	db, err := sql.Open("sqlite", "file:"+p+"?mode=ro")
	if err != nil {
		return usage.Reading{}, fmt.Errorf("accounts: open Windsurf state: %w", err)
	}
	defer func() { _ = db.Close() }()
	var raw []byte
	err = db.QueryRowContext(ctx, `SELECT value FROM ItemTable WHERE key = 'windsurf.settings.cachedPlanInfo' LIMIT 1`).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Installed, never signed in: nothing to read yet.
		return usage.Reading{}, nil
	case err != nil:
		return usage.Reading{}, fmt.Errorf("accounts: read Windsurf state: %w", err)
	}
	plan, err := decodeWindsurfPlan(raw)
	if err != nil {
		return usage.Reading{}, err
	}
	return plan.reading(), nil
}

// decodeWindsurfPlan reads the cached plan, stored as UTF-8 text or a
// UTF-16LE blob.
func decodeWindsurfPlan(raw []byte) (windsurfCachedPlan, error) {
	var plan windsurfCachedPlan
	candidates := []string{string(raw)}
	if len(raw)%2 == 0 {
		u := make([]uint16, len(raw)/2)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(raw[2*i:])
		}
		candidates = append(candidates, string(utf16.Decode(u)))
	}
	for _, c := range candidates {
		c = strings.TrimFunc(c, func(r rune) bool { return r < 0x20 || r == 0xfeff })
		if json.Unmarshal([]byte(c), &plan) == nil {
			return plan, nil
		}
	}
	return windsurfCachedPlan{}, fmt.Errorf("accounts: Windsurf's cached plan in a shape this version cannot read")
}

// reading maps the cached plan: the daily and weekly quota when Windsurf
// records it, else the message and flow-action allowances.
func (p windsurfCachedPlan) reading() usage.Reading {
	r := usage.Reading{Scope: "account", Subscription: true}
	q, u := p.QuotaUsage, p.Usage
	switch {
	case q != nil && q.DailyRemainingPercent != nil:
		r.Windows = append(r.Windows, remainingWindow("day", 24*time.Hour, *q.DailyRemainingPercent, q.DailyResetAtUnix))
	case u != nil:
		if w, ok := countWindow("month (messages)", u.Messages, u.UsedMessages, u.RemainingMessages, p.EndTimestamp); ok {
			r.Windows = append(r.Windows, w)
		}
	}
	switch {
	case q != nil && q.WeeklyRemainingPercent != nil:
		r.Windows = append(r.Windows, remainingWindow("week", 7*24*time.Hour, *q.WeeklyRemainingPercent, q.WeeklyResetAtUnix))
	case u != nil:
		if w, ok := countWindow("month (flow actions)", u.FlowActions, u.UsedFlowActions, u.RemainingFlowActions, p.EndTimestamp); ok {
			r.Windows = append(r.Windows, w)
		}
	}
	return r
}

// remainingWindow is a window from the share remaining, reset at unix
// seconds.
func remainingWindow(name string, d time.Duration, remaining float64, resetUnix int64) usage.Window {
	w := usage.Window{Name: name, UsedPct: math.Max(0, math.Min(100-remaining, 100)), Duration: d}
	if resetUnix > 0 {
		w.ResetsAt = time.Unix(resetUnix, 0).UTC()
	}
	return w
}

// countWindow is an allowance of the plan period: used of total, or total
// less remaining. The period ends at endMillis.
func countWindow(name string, total, used, remaining *int64, endMillis int64) (usage.Window, bool) {
	if total == nil || *total <= 0 {
		return usage.Window{}, false
	}
	var n int64
	switch {
	case used != nil:
		n = *used
	case remaining != nil:
		n = max(0, *total-*remaining)
	default:
		return usage.Window{}, false
	}
	n = min(max(n, 0), *total)
	w := usage.Window{Name: name, UsedPct: pct(float64(n), float64(*total))}
	if endMillis > 0 {
		w.ResetsAt = time.UnixMilli(endMillis).UTC()
	}
	return w, true
}

// readerWindsurf registers the web reader (readers_gen.go).
func readerWindsurf() usage.Reader { return Windsurf{} }

// Windsurf reads the daily and weekly quota from windsurf.com's
// GetPlanStatus (ConnectRPC, binary protobuf), the endpoint its own
// profile page calls; it is not published. The credential is the web
// session bundle the page keeps in localStorage: devin_session_token,
// devin_auth1_token, devin_account_id and devin_primary_org_id, as JSON.
type Windsurf struct {
	BaseURL string
	HTTP    *http.Client
}

func (Windsurf) Endpoint() string               { return "windsurf" }
func (Windsurf) Provider() eventschema.Provider { return "windsurf" }
func (Windsurf) Source() string                 { return "windsurf-web" }

// windsurfSession is the session bundle.
type windsurfSession struct {
	SessionToken, Auth1Token, AccountID, PrimaryOrgID string
}

// parseWindsurfSession reads the bundle as JSON, or as key=value pairs, by
// the names the page stores them under or their camel-case forms.
func parseWindsurfSession(s string) (windsurfSession, bool) {
	fields := map[string]string{}
	var m map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(s)), &m) == nil {
		for k, v := range m {
			if str, ok := v.(string); ok {
				fields[k] = strings.TrimSpace(str)
			}
		}
	} else {
		for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ',' || r == ';' }) {
			k, v, ok := strings.Cut(part, "=")
			if !ok {
				k, v, ok = strings.Cut(part, ":")
			}
			if ok {
				fields[strings.Trim(strings.TrimSpace(k), `"`)] = strings.Trim(strings.TrimSpace(v), `"`)
			}
		}
	}
	pick := func(names ...string) string {
		for _, n := range names {
			if v := fields[n]; v != "" {
				return v
			}
		}
		return ""
	}
	sess := windsurfSession{
		SessionToken: pick("devin_session_token", "devinSessionToken", "sessionToken"),
		Auth1Token:   pick("devin_auth1_token", "devinAuth1Token", "auth1Token"),
		AccountID:    pick("devin_account_id", "devinAccountId", "accountID", "accountId"),
		PrimaryOrgID: pick("devin_primary_org_id", "devinPrimaryOrgId", "primaryOrgID", "primaryOrgId"),
	}
	return sess, sess.SessionToken != "" && sess.Auth1Token != "" && sess.AccountID != "" && sess.PrimaryOrgID != ""
}

const windsurfPlanStatus = "/_backend/exa.seat_management_pb.SeatManagementService/GetPlanStatus"

func (w Windsurf) Read(ctx context.Context, key string) (usage.Reading, error) {
	sess, ok := parseWindsurfSession(key)
	if !ok {
		return usage.Reading{}, fmt.Errorf("%w (the Windsurf session needs devin_session_token, devin_auth1_token, devin_account_id and devin_primary_org_id)", usage.ErrAuth)
	}
	// GetPlanStatusRequest{auth_token: 1, include_top_up_status: 2}.
	body := protoString(nil, 1, sess.SessionToken)
	body = append(body, 2<<3, 1) // field 2, varint: true
	root := base(w.BaseURL, "https://windsurf.com")
	resp, err := send(ctx, w.HTTP, http.MethodPost, root+windsurfPlanStatus, map[string]string{
		"Content-Type":             "application/proto",
		"Connect-Protocol-Version": "1",
		"Origin":                   "https://windsurf.com",
		"Referer":                  "https://windsurf.com/profile",
		"x-auth-token":             sess.SessionToken,
		"x-devin-session-token":    sess.SessionToken,
		"x-devin-auth1-token":      sess.Auth1Token,
		"x-devin-account-id":       sess.AccountID,
		"x-devin-primary-org-id":   sess.PrimaryOrgID,
	}, body)
	if err != nil {
		return usage.Reading{}, err
	}
	status, err := decodeWindsurfPlanStatus(resp)
	if err != nil {
		return usage.Reading{}, err
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	if status.daily != nil {
		r.Windows = append(r.Windows, remainingWindow("day", 24*time.Hour, float64(*status.daily), status.dailyReset))
	}
	if status.weekly != nil {
		r.Windows = append(r.Windows, remainingWindow("week", 7*24*time.Hour, float64(*status.weekly), status.weeklyReset))
	}
	return r, nil
}

// windsurfPlanStatus is the part of PlanStatus this reader keeps.
type windsurfStatus struct {
	plan                    string
	daily, weekly           *uint64
	dailyReset, weeklyReset int64
}

// decodeWindsurfPlanStatus reads GetPlanStatusResponse{plan_status: 1}:
// PlanStatus{plan_info: 1 {plan_name: 2}, daily_quota_remaining_percent:
// 14, weekly_quota_remaining_percent: 15, daily_quota_reset_at_unix: 17,
// weekly_quota_reset_at_unix: 18}.
func decodeWindsurfPlanStatus(b []byte) (windsurfStatus, error) {
	var s windsurfStatus
	found := false
	err := protoFields(b, func(field int, wire int, v uint64, data []byte) error {
		if field != 1 || wire != 2 {
			return nil
		}
		found = true
		return protoFields(data, func(field int, wire int, v uint64, data []byte) error {
			switch {
			case field == 1 && wire == 2:
				return protoFields(data, func(field int, wire int, _ uint64, data []byte) error {
					if field == 2 && wire == 2 {
						s.plan = string(data)
					}
					return nil
				})
			case field == 14 && wire == 0:
				s.daily = &v
			case field == 15 && wire == 0:
				s.weekly = &v
			case field == 17 && wire == 0:
				s.dailyReset = int64(v) //nolint:gosec // unix seconds
			case field == 18 && wire == 0:
				s.weeklyReset = int64(v) //nolint:gosec // unix seconds
			}
			return nil
		})
	})
	if err != nil || !found {
		return windsurfStatus{}, fmt.Errorf("accounts: Windsurf plan status in a shape this version cannot read")
	}
	return s, nil
}

// protoString appends a length-delimited string field.
func protoString(b []byte, field int, s string) []byte {
	b = binary.AppendUvarint(b, uint64(field)<<3|2) //nolint:gosec // field numbers are small
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

// protoFields walks a protobuf message's fields: varints (wire 0) as v,
// length-delimited fields (wire 2) as data; fixed 32/64-bit fields are
// skipped. Groups are an error.
func protoFields(b []byte, f func(field, wire int, v uint64, data []byte) error) error {
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			return errors.New("protobuf: bad tag")
		}
		b = b[n:]
		field, wire := int(tag>>3), int(tag&7) //nolint:gosec // bounded by the varint
		var v uint64
		var data []byte
		switch wire {
		case 0:
			if v, n = binary.Uvarint(b); n <= 0 {
				return errors.New("protobuf: bad varint")
			}
			b = b[n:]
		case 1:
			if len(b) < 8 {
				return errors.New("protobuf: short fixed64")
			}
			b = b[8:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || uint64(len(b)-n) < l {
				return errors.New("protobuf: bad length")
			}
			data, b = b[n:n+int(l)], b[n+int(l):] //nolint:gosec // checked against len(b)
		case 5:
			if len(b) < 4 {
				return errors.New("protobuf: short fixed32")
			}
			b = b[4:]
		default:
			return fmt.Errorf("protobuf: wire type %d", wire)
		}
		if err := f(field, wire, v, data); err != nil {
			return err
		}
	}
	return nil
}
