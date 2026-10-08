package teamserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/team"
	"go.klarlabs.de/tokenops/internal/teamserver"
	"go.klarlabs.de/tokenops/internal/teamserver/pgstore"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// The differencing attacks of ADR 0012 §3, run against the API as a
// curious member would: every query they can make, then arithmetic.

// differencingEnv: ten people in two teams on acme/api all week, and Xena,
// in platform, who works alone on acme/secret on Tuesday and alone on
// Saturday.
func differencingEnv(t *testing.T) (*env, teamwire.EnrollResponse) {
	t.Helper()
	e := newEnv(t)
	kinds := []string{"edit", "research", "lookup"}
	for i, name := range []string{"Ann", "Ben", "Cem", "Dan", "Eva", "Fay", "Gus", "Hal", "Ida", "Jon"} {
		teamName := "platform"
		if i >= 5 {
			teamName = "web"
		}
		m := enrollDirect(t, e, teamName, name)
		var bs []teamwire.Bucket
		for d := 0; d < 5; d++ {
			bs = append(bs, teamwire.Bucket{Day: dayOf(d), Repo: "acme/api", Kind: kinds[(i+d)%3], Instructions: int64(3 + i + d), Tokens: 1000})
		}
		if code, body := e.do("POST", "/api/v1/ingest", m.DeviceToken, upload(bs...)); code != http.StatusOK {
			t.Fatalf("ingest: %d %s", code, body)
		}
	}
	x := enrollDirect(t, e, "platform", "Xena")
	if code, body := e.do("POST", "/api/v1/ingest", x.DeviceToken, upload(
		teamwire.Bucket{Day: dayOf(1), Repo: "acme/secret", Kind: "edit", Instructions: 17, Tokens: 1000},
		teamwire.Bucket{Day: dayOf(5), Repo: "acme/api", Kind: "edit", Instructions: 23, Tokens: 1000})); code != http.StatusOK {
		t.Fatalf("ingest: %d %s", code, body)
	}
	return e, x
}

// enrollDirect joins through the store, past the enrolment rate limit.
func enrollDirect(t *testing.T, e *env, teamName, name string) teamwire.EnrollResponse {
	t.Helper()
	ctx := context.Background()
	org, err := e.store.OrgByName(ctx, "Acme")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.TeamByRef(ctx, org.ID, teamName); err != nil {
		if _, err := e.store.CreateTeam(ctx, pgstore.Console(org.ID), teamName); err != nil {
			t.Fatal(err)
		}
	}
	inv, _, err := e.store.CreateInvite(ctx, pgstore.Console(org.ID), teamName, team.RoleMember, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.store.Enroll(ctx, inv, name, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func aggregates(t *testing.T, e *env, token, query string) teamserver.AggregateAnswer {
	t.Helper()
	code, body := e.do("GET", "/api/v1/aggregates?"+query, token, nil)
	if code != http.StatusOK {
		t.Fatalf("%s: %d %s", query, code, body)
	}
	var a teamserver.AggregateAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func rowsByKey(a teamserver.AggregateAnswer) map[string]teamserver.AggregateRow {
	out := map[string]teamserver.AggregateRow{}
	for _, r := range a.Rows {
		out[r.PeriodStart+"|"+r.Group] = r
	}
	return out
}

// Everything a member can ask, by dimension and period, over the whole week.
func everything(t *testing.T, e *env, token string) map[string]map[string]teamserver.AggregateRow {
	t.Helper()
	week := releasedWeek()
	out := map[string]map[string]teamserver.AggregateRow{}
	for _, by := range []string{"team", "repo", "kind"} {
		for _, p := range []string{"day", "week"} {
			q := fmt.Sprintf("by=%s&period=%s&since=%s&until=%s", by, p, dayOf(0), week.AddDate(0, 0, 7).Format(teamwire.DayLayout))
			out[by+"|"+p] = rowsByKey(aggregates(t, e, token, q))
		}
	}
	return out
}

func TestDifferencingAttacksGetNothing(t *testing.T) {
	e, x := differencingEnv(t)
	member := enrollDirect(t, e, "web", "Mallory") // the curious reader
	all := everything(t, e, member.DeviceToken)
	tue, sat := dayOf(1), dayOf(5)
	sum := func(rows map[string]teamserver.AggregateRow, day string) (visible int64, hidden int) {
		for _, r := range rows {
			if r.PeriodStart != day {
				continue
			}
			if r.Suppressed {
				hidden++
			} else {
				visible += r.Totals.Instructions
			}
		}
		return visible, hidden
	}

	// Primary: Xena's cells are withheld.
	if r := all["repo|day"][tue+"|acme/secret"]; !r.Suppressed || r.People != 0 || r.Totals.Instructions != 0 {
		t.Errorf("Xena's repository shown: %+v", r)
	}
	if r := all["repo|day"][sat+"|acme/api"]; !r.Suppressed {
		t.Errorf("Xena's Saturday shown: %+v", r)
	}

	// Suppression is targeted: most of the week still shows.
	shown := 0
	for _, rows := range all {
		for _, r := range rows {
			if !r.Suppressed {
				shown++
			}
		}
	}
	if shown < 15 {
		t.Errorf("only %d cells shown", shown)
	}
	t.Logf("%d cells shown", shown)

	// Complement / total-minus-visible: Tuesday's total from one
	// dimension, minus Tuesday's visible cells of another.
	for _, pair := range [][2]string{{"kind|day", "repo|day"}, {"team|day", "repo|day"}, {"repo|day", "kind|day"}} {
		totalRows, partRows := all[pair[0]], all[pair[1]]
		total, totalHidden := sum(totalRows, tue)
		part, partHidden := sum(partRows, tue)
		if totalHidden == 0 && partHidden > 0 && total-part == 17 {
			t.Errorf("%s total %d - visible %s %d = Xena's 17 on acme/secret", pair[0], total, pair[1], part)
		}
	}

	// Over time: the week minus its visible days.
	for _, by := range []string{"repo", "kind", "team"} {
		for key, week := range all[by+"|week"] {
			if week.Suppressed {
				continue
			}
			group := key[len(week.PeriodStart)+1:]
			var days int64
			for d := 0; d < 7; d++ {
				if r, ok := all[by+"|day"][dayOf(d)+"|"+group]; ok && !r.Suppressed {
					days += r.Totals.Instructions
				}
			}
			if rest := week.Totals.Instructions - days; rest == 23 || rest == 17 || rest == 40 {
				t.Errorf("%s %s: week %d - visible days %d = %d, Xena's alone", by, group, week.Totals.Instructions, days, rest)
			}
		}
	}

	// Overlapping windows: any window gives the same fixed cells, and a
	// week cell is always the whole week.
	for _, by := range []string{"team", "repo", "kind"} {
		for _, w := range [][2]string{{dayOf(1), dayOf(3)}, {dayOf(2), dayOf(6)}, {dayOf(1), dayOf(2)}} {
			got := rowsByKey(aggregates(t, e, member.DeviceToken, fmt.Sprintf("by=%s&period=day&since=%s&until=%s", by, w[0], w[1])))
			for k, r := range got {
				if !reflect.DeepEqual(r, all[by+"|day"][k]) {
					t.Errorf("%s %s in window %v differs from the full week's: %+v vs %+v", by, k, w, r, all[by+"|day"][k])
				}
			}
			weekly := rowsByKey(aggregates(t, e, member.DeviceToken, fmt.Sprintf("by=%s&period=week&since=%s&until=%s", by, w[0], dayOf(7))))
			if !reflect.DeepEqual(weekly, all[by+"|week"]) {
				t.Errorf("%s week cells for a window from %s differ: %+v", by, w[0], weekly)
			}
		}
	}

	// Over time, by uploading or leaving after the week was released:
	// nothing changes.
	if code, body := e.do("POST", "/api/v1/ingest", x.DeviceToken, upload(
		teamwire.Bucket{Day: tue, Repo: "acme/api", Kind: "deep", Instructions: 99, Tokens: 5})); code != http.StatusOK {
		t.Fatalf("late upload: %d %s", code, body)
	}
	if after := everything(t, e, member.DeviceToken); !reflect.DeepEqual(after, all) {
		t.Error("a late upload changed released cells")
	}
	if code, _ := e.do("DELETE", "/api/v1/devices/self", x.DeviceToken, nil); code != http.StatusOK {
		t.Fatal("leave")
	}
	if after := everything(t, e, member.DeviceToken); !reflect.DeepEqual(after, all) {
		t.Error("a member leaving changed released cells")
	}
}
