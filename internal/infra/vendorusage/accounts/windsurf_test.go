package accounts

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf16"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// windsurfStore writes a state.vscdb holding value under the cached plan's
// key, as Windsurf does.
func windsurfStore(t *testing.T, value any) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "state.vscdb")
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`); err != nil {
		t.Fatal(err)
	}
	if value != nil {
		if _, err := db.Exec(`INSERT INTO ItemTable VALUES ('windsurf.settings.cachedPlanInfo', ?)`, value); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestWindsurfLocalReadsTheCachedQuota(t *testing.T) {
	got, err := WindsurfLocal{Path: windsurfStore(t, fixture(t, "windsurf"))}.Read(context.Background(), "")
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "day" || !approx(w.UsedPct, 91) || !w.ResetsAt.Equal(time.Unix(1774080000, 0)) {
		t.Errorf("daily %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 46) || w.Duration != 7*24*time.Hour {
		t.Errorf("weekly %+v", w)
	}
}

// Newer caches carry only counters, sometimes as a UTF-16 blob.
func TestWindsurfLocalCounters(t *testing.T) {
	const counters = `{"planName": "Pro","usage": {"messages": 50000,"usedMessages": 1200,"remainingMessages": 48800,"flowActions": 150000,"usedFlowActions": 0,"remainingFlowActions": 150000}}`
	u := utf16.Encode([]rune(counters))
	blob := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(blob[2*i:], c)
	}
	got, err := WindsurfLocal{Path: windsurfStore(t, blob)}.Read(context.Background(), "")
	if err != nil || len(got.Windows) != 2 || !approx(got.Windows[0].UsedPct, 2.4) || got.Windows[1].UsedPct != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
	inferred, err := WindsurfLocal{Path: windsurfStore(t, `{"planName": "Pro","usage": {"messages": 100,"remainingMessages": 25}}`)}.Read(context.Background(), "")
	if err != nil || len(inferred.Windows) != 1 || !approx(inferred.Windows[0].UsedPct, 75) {
		t.Errorf("inferred %+v, %v", inferred, err)
	}
	free, err := WindsurfLocal{Path: windsurfStore(t, `{"planName": "Free"}`)}.Read(context.Background(), "")
	if err != nil || !free.Empty() {
		t.Errorf("free %+v, %v", free, err)
	}
	if _, err := (WindsurfLocal{Path: windsurfStore(t, "not json")}).Read(context.Background(), ""); err == nil {
		t.Error("an unreadable cache read without error")
	}
}

func TestWindsurfLocalNotInstalled(t *testing.T) {
	if _, err := (WindsurfLocal{Path: filepath.Join(t.TempDir(), "state.vscdb")}).Read(context.Background(), ""); !errors.Is(err, usage.ErrNotInstalled) {
		t.Errorf("err %v", err)
	}
	if got, err := (WindsurfLocal{Path: windsurfStore(t, nil)}).Read(context.Background(), ""); err != nil || !got.Empty() {
		t.Errorf("signed out: %+v, %v", got, err)
	}
}

// windsurfPlanStatus is GetPlanStatusResponse for plan "Pro", daily 68%
// and weekly 84% remaining, as CodexBar's tests build it.
const windsurfPlanStatusHex = "0a210a05120350726f1a060880d6e1cf06704478548801e0b3e2cf06900180c1e8cf06"

const windsurfBundle = `{"devin_session_token":"devin-session-token$abc","devin_auth1_token":"auth1","devin_account_id":"acct","devin_primary_org_id":"org"}`

func TestWindsurfWebReadsPlanStatus(t *testing.T) {
	resp, _ := hex.DecodeString(windsurfPlanStatusHex)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("x-auth-token") != "devin-session-token$abc" || r.Header.Get("x-devin-primary-org-id") != "org" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("unauthorized"))
			return
		}
		if r.URL.Path != windsurfPlanStatus || r.Header.Get("Content-Type") != "application/proto" ||
			hex.EncodeToString(body) != "0a17646576696e2d73657373696f6e2d746f6b656e246162631001" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write(resp)
	}))
	defer srv.Close()
	got, err := Windsurf{BaseURL: srv.URL}.Read(context.Background(), windsurfBundle)
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "day" || !approx(w.UsedPct, 32) || !w.ResetsAt.Equal(time.Unix(1777900000, 0)) {
		t.Errorf("daily %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 16) || !w.ResetsAt.Equal(time.Unix(1778000000, 0)) {
		t.Errorf("weekly %+v", w)
	}
	stale := `{"devin_session_token":"old","devin_auth1_token":"a","devin_account_id":"b","devin_primary_org_id":"org"}`
	if _, err := (Windsurf{BaseURL: srv.URL}).Read(context.Background(), stale); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("stale session: %v", err)
	}
	if _, err := (Windsurf{BaseURL: srv.URL}).Read(context.Background(), `{"devin_session_token":"x"}`); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("partial bundle: %v", err)
	}
}

func TestWindsurfSessionForms(t *testing.T) {
	for _, s := range []string{
		windsurfBundle,
		"devinSessionToken=a; auth1Token=b; accountId=c; primaryOrgId=d",
	} {
		if _, ok := parseWindsurfSession(s); !ok {
			t.Errorf("%q not read", s)
		}
	}
	if _, err := decodeWindsurfPlanStatus([]byte{0x0b}); err == nil {
		t.Error("a group decoded")
	}
	if _, err := decodeWindsurfPlanStatus(nil); err == nil {
		t.Error("an empty answer decoded")
	}
}
