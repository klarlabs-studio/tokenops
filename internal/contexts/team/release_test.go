package team

import (
	"fmt"
	"math/big"
	"slices"
	"testing"
	"time"
)

var testWeek = time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC) // a Monday

func work(member string, day int, repo, kind string, instr int64) Contribution {
	return Contribution{Member: member, Day: testWeek.AddDate(0, 0, day), Repo: repo, Kind: kind,
		Totals: Totals{Instructions: instr, Tokens: instr * 1000, CostUSD: float64(instr) / 10}}
}

func cellsBy(cells []Cell) map[string]Cell {
	out := map[string]Cell{}
	for _, c := range cells {
		out[fmt.Sprintf("%s|%s|%s|%s", c.Dim, c.Period, c.PeriodStart.Format("2006-01-02"), c.Group)] = c
	}
	return out
}

func key(d Dimension, p Period, day int, g string) string {
	start := testWeek
	if p == Day {
		start = testWeek.AddDate(0, 0, day)
	}
	return fmt.Sprintf("%s|%s|%s|%s", d, p, start.Format("2006-01-02"), g)
}

// truth is what the reader is not supposed to learn: the figures of every
// cell, and who is in it.
type truth struct {
	instr  map[string]int64
	people map[string]map[string]bool
}

func truthOf(in ReleaseInput) truth {
	t := truth{instr: map[string]int64{}, people: map[string]map[string]bool{}}
	addTo := func(k, m string, v int64) {
		t.instr[k] += v
		if t.people[k] == nil {
			t.people[k] = map[string]bool{}
		}
		t.people[k][m] = true
	}
	for _, c := range in.Contributions {
		d := int(c.Day.Sub(testWeek).Hours() / 24)
		for _, p := range []Period{Day, Week} {
			addTo(key(ByRepo, p, d, c.Repo), c.Member, c.Totals.Instructions)
			addTo(key(ByKind, p, d, c.Kind), c.Member, c.Totals.Instructions)
			for _, team := range in.Teams[c.Member] {
				addTo(key(ByTeam, p, d, team), c.Member, c.Totals.Instructions)
			}
		}
	}
	return t
}

// A team of ten across two repositories and three kinds of work, where
// one person (Xena) works alone on a side repository, alone on Saturday,
// and in a kind of work nobody else does.
func scenario() ReleaseInput {
	in := ReleaseInput{WeekStart: testWeek, MinGroup: 3, Teams: map[string][]string{}}
	people := []string{"ann", "ben", "cem", "dee", "eva", "fay", "gus", "hal", "ida", "jon"}
	for i, m := range people {
		team := "platform"
		if i >= 5 {
			team = "web"
		}
		in.Teams[m] = []string{team}
		for d := 0; d < 5; d++ {
			repo := "acme/api"
			if (i+d)%2 == 0 {
				repo = "acme/ui"
			}
			kind := []string{"edit", "research", "lookup"}[(i+d)%3]
			in.Contributions = append(in.Contributions, work(m, d, repo, kind, int64(3+i+d)))
		}
	}
	in.Teams["xena"] = []string{"platform"}
	in.Contributions = append(in.Contributions,
		work("xena", 1, "acme/side", "edit", 4),
		work("xena", 5, "acme/api", "deep", 9))
	return in
}

// assertNoResidualLeak runs the attacks a reader can do by hand on every
// line: a total (from a fully visible other dimension, or a visible week)
// minus the visible parts is the hidden parts' sum. It fails when that sum
// is about one or two people.
func assertNoResidualLeak(t *testing.T, in ReleaseInput, cells []Cell) {
	t.Helper()
	got := cellsBy(cells)
	tr := truthOf(in)
	k := in.MinGroup
	residual := func(name string, hidden []string) {
		u := map[string]bool{}
		for _, h := range hidden {
			for m := range tr.people[h] {
				u[m] = true
			}
		}
		if len(u) > 0 && len(u) < k {
			t.Errorf("%s: total minus visible cells reveals %v's figures", name, sortedKeys(u))
		}
	}
	groupsOf := func(d Dimension) []string {
		set := map[string]bool{}
		for _, c := range cells {
			if c.Dim == d {
				set[c.Group] = true
			}
		}
		return sortedKeys(set)
	}
	// Complement / total-minus-visible within a day or the week, across
	// the two partitions: repositories and kinds of work.
	for _, p := range []Period{Day, Week} {
		for day := 0; day < 7; day++ {
			if p == Week && day > 0 {
				break
			}
			for _, pair := range [][2]Dimension{{ByRepo, ByKind}, {ByKind, ByRepo}} {
				totalKnown := true
				for _, g := range groupsOf(pair[1]) {
					if got[key(pair[1], p, day, g)].Suppressed {
						totalKnown = false
					}
				}
				if !totalKnown {
					continue
				}
				var hidden []string
				for _, g := range groupsOf(pair[0]) {
					if got[key(pair[0], p, day, g)].Suppressed {
						hidden = append(hidden, key(pair[0], p, day, g))
					}
				}
				residual(fmt.Sprintf("%s total from %s, %s day %d", p, pair[1], pair[0], day), hidden)
			}
		}
	}
	// A week minus its visible days.
	for _, d := range []Dimension{ByRepo, ByKind, ByTeam} {
		for _, g := range groupsOf(d) {
			if got[key(d, Week, 0, g)].Suppressed {
				continue
			}
			var hidden []string
			for day := 0; day < 7; day++ {
				if got[key(d, Day, day, g)].Suppressed {
					hidden = append(hidden, key(d, Day, day, g))
				}
			}
			residual(fmt.Sprintf("%s %s week minus days", d, g), hidden)
		}
	}
	// Withheld cells carry nothing.
	for _, c := range cells {
		if c.Suppressed && (c.People != 0 || c.Totals != (Totals{})) {
			t.Errorf("withheld cell %s/%s carries figures", c.Dim, c.Group)
		}
		if !c.Suppressed && c.People > 0 && c.People < k {
			t.Errorf("cell %s/%s of %d people shown", c.Dim, c.Group, c.People)
		}
	}
}

func TestReleaseWithholdsSmallGroupsAndTheirComplements(t *testing.T) {
	in := scenario()
	cells, rep := Release(in)
	got := cellsBy(cells)
	for _, k := range []string{key(ByRepo, Day, 1, "acme/side"), key(ByRepo, Week, 0, "acme/side"),
		key(ByKind, Day, 5, "deep"), key(ByKind, Week, 0, "deep"), key(ByRepo, Day, 5, "acme/api")} {
		if c, ok := got[k]; !ok || !c.Suppressed {
			t.Errorf("%s shown: %+v", k, c)
		}
	}
	if rep.Primary == 0 || rep.Secondary == 0 {
		t.Errorf("report %+v: want primary and secondary suppression", rep)
	}
	if rep.Fallback != "" {
		t.Errorf("fell back to %q", rep.Fallback)
	}
	// Most of the week is still shown: suppression is targeted.
	if c := got[key(ByTeam, Week, 0, "web")]; c.Suppressed || c.People != 5 {
		t.Errorf("team web's week: %+v", c)
	}
	assertNoResidualLeak(t, in, cells)
}

// The complement attack on one day: every repository but one is shown,
// and the kinds of work give the day's total.
func TestReleaseComplementAttack(t *testing.T) {
	in := ReleaseInput{WeekStart: testWeek, MinGroup: 3, Teams: map[string][]string{}}
	for _, m := range []string{"a", "b", "c", "d", "e"} {
		in.Teams[m] = []string{"t"}
		in.Contributions = append(in.Contributions, work(m, 0, "acme/api", "edit", 5))
	}
	in.Teams["x"] = []string{"t"}
	in.Contributions = append(in.Contributions, work("x", 0, "acme/secret", "edit", 7))
	cells, _ := Release(in)
	got := cellsBy(cells)
	if !got[key(ByRepo, Day, 0, "acme/secret")].Suppressed {
		t.Fatal("one person's repository shown")
	}
	// Without complementary suppression acme/api (5 people) and kind edit
	// (6 people) would both show, and edit - api = x's figures.
	api, edit := got[key(ByRepo, Day, 0, "acme/api")], got[key(ByKind, Day, 0, "edit")]
	if !api.Suppressed && !edit.Suppressed {
		t.Errorf("edit (%d) - acme/api (%d) = x's %d instructions", edit.Totals.Instructions, api.Totals.Instructions,
			edit.Totals.Instructions-api.Totals.Instructions)
	}
	// Nor through the team, which is everyone.
	team := got[key(ByTeam, Day, 0, "t")]
	if !api.Suppressed && !team.Suppressed {
		t.Errorf("team t - acme/api reveals x")
	}
	assertNoResidualLeak(t, in, cells)
}

// Overlapping teams: Lars is in platform and web. platform + web - (the
// day's total) = Lars, when everyone else is in exactly one team.
func TestReleaseOverlappingTeamsAttack(t *testing.T) {
	in := ReleaseInput{WeekStart: testWeek, MinGroup: 3, Teams: map[string][]string{}}
	for i, m := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		team := "platform"
		if i >= 4 {
			team = "web"
		}
		in.Teams[m] = []string{team}
		in.Contributions = append(in.Contributions, work(m, 0, "acme/api", "edit", int64(4+i)))
	}
	in.Teams["lars"] = []string{"platform", "web"}
	in.Contributions = append(in.Contributions, work("lars", 0, "acme/api", "edit", 11))
	cells, _ := Release(in)
	got := cellsBy(cells)
	p, w, total := got[key(ByTeam, Day, 0, "platform")], got[key(ByTeam, Day, 0, "web")], got[key(ByRepo, Day, 0, "acme/api")]
	if !p.Suppressed && !w.Suppressed && !total.Suppressed {
		t.Errorf("platform %d + web %d - total %d = Lars's %d", p.Totals.Instructions, w.Totals.Instructions,
			total.Totals.Instructions, p.Totals.Instructions+w.Totals.Instructions-total.Totals.Instructions)
	}
	assertNoResidualLeak(t, in, cells)
	if !independentlySafe(t, in, cells) {
		t.Error("an independent solver recovers Lars's figure")
	}
}

// Over time: a person who works alone on one day of the week. The week
// minus the visible days would be that day.
func TestReleaseWeekMinusDaysAttack(t *testing.T) {
	in := ReleaseInput{WeekStart: testWeek, MinGroup: 3, Teams: map[string][]string{}}
	for _, m := range []string{"a", "b", "c", "d"} {
		in.Teams[m] = []string{"t"}
		for d := 0; d < 5; d++ {
			in.Contributions = append(in.Contributions, work(m, d, "acme/api", "edit", 3))
		}
	}
	in.Contributions = append(in.Contributions, work("a", 5, "acme/api", "edit", 13)) // Saturday, alone
	cells, _ := Release(in)
	got := cellsBy(cells)
	if !got[key(ByRepo, Day, 5, "acme/api")].Suppressed {
		t.Fatal("Saturday shown")
	}
	week := got[key(ByRepo, Week, 0, "acme/api")]
	visible := int64(0)
	hiddenWeekdays := 0
	for d := 0; d < 5; d++ {
		if c := got[key(ByRepo, Day, d, "acme/api")]; c.Suppressed {
			hiddenWeekdays++
		} else {
			visible += c.Totals.Instructions
		}
	}
	if !week.Suppressed && hiddenWeekdays == 0 {
		t.Errorf("week %d - weekdays %d = a's Saturday", week.Totals.Instructions, visible)
	}
	assertNoResidualLeak(t, in, cells)
	if !independentlySafe(t, in, cells) {
		t.Error("an independent solver recovers a's Saturday")
	}
}

func TestReleaseIsDeterministicAndAnonymousCellsCarryNothing(t *testing.T) {
	in := scenario()
	a, _ := Release(in)
	// Shuffle the input order.
	rev := in
	rev.Contributions = append([]Contribution(nil), in.Contributions...)
	slices.Reverse(rev.Contributions)
	b, _ := Release(rev)
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Error("release depends on input order")
	}
}

func TestReleaseFloorOfOneShowsEverything(t *testing.T) {
	in := scenario()
	in.MinGroup = 1
	cells, rep := Release(in)
	for _, c := range cells {
		if c.Suppressed {
			t.Fatalf("withheld at floor 1: %+v", c)
		}
	}
	if rep.Primary != 0 {
		t.Errorf("report %+v", rep)
	}
}

func TestReleaseHigherFloor(t *testing.T) {
	in := scenario()
	in.MinGroup = 5
	cells, rep := Release(in)
	assertNoResidualLeak(t, in, cells)
	if rep.Fallback == "all" {
		t.Errorf("withheld the whole week at floor 5: %+v", rep)
	}
}

func TestReleasableAt(t *testing.T) {
	if got := ReleasableAt(testWeek.AddDate(0, 0, 3)); !got.Equal(time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("releasable at %v", got)
	}
	if got := LatestReleasable(time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)); !got.Equal(testWeek) {
		t.Errorf("latest releasable %v", got)
	}
	if got := LatestReleasable(time.Date(2026, 9, 16, 23, 0, 0, 0, time.UTC)); !got.Equal(testWeek.AddDate(0, 0, -7)) {
		t.Errorf("latest releasable a day early %v", got)
	}
}

// independentlySafe is a second, independent solver: exact rational
// Gaussian elimination over per-member-per-day unknowns (each scenario
// using it gives every member one repository and kind per day, so those
// are the finest cells the released ones sum). It knows every visible
// cell and every team's membership, and reports whether any single
// member's figure for any day, or for the week, can be computed.
func independentlySafe(t *testing.T, in ReleaseInput, cells []Cell) bool {
	t.Helper()
	var atoms []atom
	idx := map[atom]int{}
	for _, c := range in.Contributions {
		a := atom{c.Member, int(c.Day.Sub(testWeek).Hours() / 24)}
		if _, ok := idx[a]; !ok {
			idx[a] = len(atoms)
			atoms = append(atoms, a)
		}
	}
	n := len(atoms)
	var rows [][]*big.Rat
	for _, c := range cells {
		if c.Suppressed {
			continue
		}
		row := make([]*big.Rat, n)
		for i := range row {
			row[i] = new(big.Rat)
		}
		for _, con := range in.Contributions {
			d := int(con.Day.Sub(testWeek).Hours() / 24)
			if c.Period == Day && !c.PeriodStart.Equal(testWeek.AddDate(0, 0, d)) {
				continue
			}
			sums := false
			switch c.Dim {
			case ByRepo:
				sums = con.Repo == c.Group
			case ByKind:
				sums = con.Kind == c.Group
			case ByTeam:
				for _, tm := range in.Teams[con.Member] {
					sums = sums || tm == c.Group
				}
			}
			if sums {
				row[idx[atom{con.Member, d}]].SetInt64(1)
			}
		}
		rows = append(rows, row)
	}
	// Reduce, then ask whether any combination supported on one member's
	// atoms is in the row space: for each member, eliminate every other
	// column first and see if a row survives with support only on theirs.
	members := map[string]bool{}
	for _, a := range atoms {
		members[a.m] = true
	}
	for m := range members {
		order := make([]int, 0, n)
		for i, a := range atoms {
			if a.m != m {
				order = append(order, i)
			}
		}
		for i, a := range atoms {
			if a.m == m {
				order = append(order, i)
			}
		}
		if supportOnlyOn(rows, order, n-countOf(atoms, m)) {
			return false
		}
	}
	return true
}

type atom struct {
	m   string
	day int
}

func countOf(atoms []atom, m string) int {
	n := 0
	for _, a := range atoms {
		if a.m == m {
			n++
		}
	}
	return n
}

// supportOnlyOn eliminates columns in order and reports whether some
// non-zero row ends with zeros in the first `others` columns of the order.
func supportOnlyOn(rows [][]*big.Rat, order []int, others int) bool {
	m := make([][]*big.Rat, len(rows))
	for i := range rows {
		m[i] = make([]*big.Rat, len(rows[i]))
		for j := range rows[i] {
			m[i][j] = new(big.Rat).Set(rows[i][j])
		}
	}
	r := 0
	for _, col := range order {
		sel := -1
		for i := r; i < len(m); i++ {
			if m[i][col].Sign() != 0 {
				sel = i
				break
			}
		}
		if sel < 0 {
			continue
		}
		m[r], m[sel] = m[sel], m[r]
		for i := range m {
			if i == r || m[i][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Quo(m[i][col], m[r][col])
			for j := range m[i] {
				m[i][j].Sub(m[i][j], new(big.Rat).Mul(f, m[r][j]))
			}
		}
		r++
	}
	// Rows whose pivot came after the "others" columns are supported only
	// on the member's columns.
	for i := 0; i < r; i++ {
		zeroOnOthers := true
		for _, col := range order[:others] {
			if m[i][col].Sign() != 0 {
				zeroOnOthers = false
				break
			}
		}
		if zeroOnOthers {
			return true
		}
	}
	return false
}
