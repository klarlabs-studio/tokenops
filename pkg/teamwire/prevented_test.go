package teamwire

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// preventedUpload is a valid schema 5 upload with one finished week: the
// week of Monday 2026-11-02 ended 2026-11-09, and the upload is computed
// four days after that.
func preventedUpload() Upload {
	u := validUpload()
	u.Schema = SchemaPrevented
	u.ComputedAt = time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	usd := 1.25
	u.PreventedWeeks = []string{"2026-11-02"}
	u.Prevented = []PreventedWeek{
		{Week: "2026-11-02", Mechanism: MechanismReadGuard, Count: 12, Tokens: 40_000, USD: &usd},
		{Week: "2026-11-02", Mechanism: MechanismLockout, Count: 1},
	}
	return u
}

func TestPreventedValidate(t *testing.T) {
	neg := -1.0
	tests := []struct {
		name   string
		change func(*Upload)
		want   string
	}{
		{"valid", func(*Upload) {}, ""},
		{"an older schema carries none", func(u *Upload) { u.Schema = SchemaReverts }, "carries no prevented"},
		{"a week that is not a Monday", func(u *Upload) {
			u.PreventedWeeks = []string{"2026-11-03"}
			u.Prevented = u.Prevented[:0]
		}, "not a Monday"},
		{"a week not yet finished three days", func(u *Upload) { u.ComputedAt = time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC) }, "not finished"},
		{"a row for a week not listed", func(u *Upload) { u.Prevented[0].Week = "2026-10-26" }, "not in prevented_weeks"},
		{"an unknown mechanism", func(u *Upload) { u.Prevented[0].Mechanism = "session 3f2a" }, "not a mechanism"},
		{"a row twice", func(u *Upload) { u.Prevented[1] = u.Prevented[0] }, "appears twice"},
		{"negative tokens", func(u *Upload) { u.Prevented[0].Tokens = -1 }, "negative"},
		{"negative dollars", func(u *Upload) { u.Prevented[0].USD = &neg }, "usd"},
		{"a row that counts nothing", func(u *Upload) { u.Prevented[1].Count = 0 }, "count"},
		{"blocked minutes on a lockout", func(u *Upload) { u.Prevented[1].BlockedMinutes = 300 }, ""},
		{"blocked minutes a week at most", func(u *Upload) { u.Prevented[1].BlockedMinutes = MaxBlockedMinutes }, ""},
		{"blocked minutes over a week", func(u *Upload) { u.Prevented[1].BlockedMinutes = MaxBlockedMinutes + 1 }, "blocked_minutes"},
		{"negative blocked minutes", func(u *Upload) { u.Prevented[1].BlockedMinutes = -5 }, "blocked_minutes"},
		{"blocked minutes on a read guard", func(u *Upload) { u.Prevented[0].BlockedMinutes = 5 }, "only the lockout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := preventedUpload()
			tt.change(&u)
			err := u.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("valid upload refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v; want %q", err, tt.want)
			}
		})
	}
}

// The week's figures are keyed by the week and the mechanism only: no
// session, no repository, no person.
func TestPreventedWeekJSONNames(t *testing.T) {
	usd := 0.5
	b, err := jsonOf(PreventedWeek{Week: "2026-11-02", Mechanism: MechanismFmt, Count: 3, Tokens: 900, USD: &usd})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"week":"2026-11-02","mechanism":"fmt","count":3,"tokens":900,"usd":0.5}`; b != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

func jsonOf(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// blocked_minutes is omitted when zero and named as the schema says.
func TestPreventedBlockedMinutesJSON(t *testing.T) {
	b, err := jsonOf(PreventedWeek{Week: "2026-11-02", Mechanism: MechanismLockout, Count: 2, BlockedMinutes: 420})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"week":"2026-11-02","mechanism":"lockout","count":2,"blocked_minutes":420}`; b != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

// A server below schema 5 gets schema 4 with no prevented key.
func TestForServerDropsPreventedBelowSchema5(t *testing.T) {
	u := preventedUpload().ForServer(SchemaReverts)
	if u.Schema != SchemaReverts || u.Prevented != nil || u.PreventedWeeks != nil {
		t.Fatalf("for a schema 4 server: %+v", u)
	}
	b, _ := json.Marshal(u)
	if strings.Contains(string(b), "prevented") {
		t.Errorf("a schema 4 upload carries a prevented key: %s", b)
	}
	if err := u.Validate(); err != nil {
		t.Errorf("the schema 4 upload is invalid: %v", err)
	}
}
