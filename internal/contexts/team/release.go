package team

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// A release is what the team plane shows of one ISO week (ADR 0012 §3):
// every team, repository and kind-of-work cell, per day and for the week,
// computed once, after the week has settled, and never recomputed. Every
// aggregate query is answered from releases, so asking again — with
// another window, another dimension, after more uploads — returns the same
// cells and nothing new.
//
// Suppression works on the whole week at once. Cells fewer than the
// minimum group size contributed to are withheld (primary suppression).
// Then more cells are withheld until nothing a reader can compute from the
// released cells — by adding and subtracting them, using that each
// day's repository cells, kind cells and team atoms sum to the same total,
// that a week is the sum of its days, and who is in which team — describes
// fewer than the minimum number of people (secondary suppression). The
// check is exact linear algebra, not a rule of thumb: see Release.

// SettleDays is how long after a week ends it is released. Machines upload
// hourly when running; a laptop shut on Friday evening uploads on Monday.
// Figures that arrive after their week was released are not added to it:
// changing a released cell would show the late upload on its own.
const SettleDays = 3

// ReleasableAt is when the week starting weekStart may be released.
func ReleasableAt(weekStart time.Time) time.Time {
	return Week.Start(weekStart).AddDate(0, 0, 7+SettleDays)
}

// LatestReleasable is the start of the newest week that may be released at
// now.
func LatestReleasable(now time.Time) time.Time {
	return Week.Start(now.UTC().AddDate(0, 0, -(7 + SettleDays)))
}

// Contribution is one member's figures for one day, repository and kind
// of work.
type Contribution struct {
	Member string
	// Day is the UTC day; it must fall in the release's week.
	Day    time.Time
	Repo   string
	Kind   string
	Totals Totals
}

// ReleaseInput is one week of an organisation's figures.
type ReleaseInput struct {
	WeekStart time.Time
	MinGroup  int
	// Contributions are the week's figures, one per member, day,
	// repository and kind of work (devices summed).
	Contributions []Contribution
	// Teams maps every member who could have contributed (a machine
	// enrolled before the week ended) to the names of their teams. A
	// contributor missing here is in no team.
	Teams map[string][]string
}

// Cell is one released figure set.
type Cell struct {
	Dim         Dimension
	Period      Period
	PeriodStart time.Time
	Group       string
	// People is how many members contributed; zero when withheld.
	People     int
	Totals     Totals
	Suppressed bool
}

// ReleaseReport says how a release came about, for the server's log.
type ReleaseReport struct {
	Cells, Primary, Secondary int
	// Checks counts the groups of people the exact check examined.
	Checks int
	// Fallback is set when the check ran out of budget and the release
	// fell back to withholding every day cell ("days") or everything
	// ("all").
	Fallback string
}

// Budgets of the exact check. A week whose check would exceed them is
// released more coarsely, never less safely.
const (
	maxCandidateGroups = 20000
	maxCheckWork       = 400_000_000 // roughly, field multiplications
	maxSecondaryRounds = 400
)

// periodWeek is the index of the week among a release's periods; 0..6 are
// its days.
const periodWeek = 7

type cellKey struct {
	dim    Dimension
	period int
	group  string
}

type relCell struct {
	key        cellKey
	people     map[string]bool
	totals     Totals
	suppressed bool
	primary    bool
}

// variable is an unknown of the reader's equations: a repository's or a
// kind's figures on one day, or the figures of the members sharing one
// combination of teams (a signature) on one day.
type variable struct {
	name   string
	day    int
	people map[string]bool
	// group is the repository or kind; teams lists a signature's teams.
	dim   Dimension
	group string
	teams []string
}

// Release computes the cells of one week, with every cell withheld that
// must be for no computation from them to describe fewer than
// in.MinGroup people.
//
// The reader is assumed to know every released cell, which cells are
// absent (nobody contributed), and who is in which team. Their unknowns
// are each repository's and kind's figures per day and the figures per
// day of each team signature (the members sharing one exact set of
// teams); they know that per day the repository unknowns, the kind
// unknowns and the signature unknowns each sum to the day's total, that a
// week cell is the sum of its days, and that a team cell is the sum of the
// signatures containing the team. Everything they can compute is a linear
// combination of those facts. A combination is a disclosure when it is
// informative (not an identity that holds for any figures) and involves
// only unknowns that fewer than MinGroup people contributed to in total.
// Release withholds cells until there is none, checking every group of
// fewer than MinGroup people exactly (gf.go).
func Release(in ReleaseInput) ([]Cell, ReleaseReport) {
	k := in.MinGroup
	if k < 1 {
		k = 1
	}
	week := Week.Start(in.WeekStart)
	r := newRelease(in, week)
	var rep ReleaseReport
	for _, c := range r.cells {
		if k > 1 && len(c.people) < k {
			c.suppressed, c.primary = true, true
			rep.Primary++
		}
	}
	if k > 1 {
		r.linePass(k)
		rep.Fallback, rep.Checks = r.secure(k)
	}
	out := make([]Cell, 0, len(r.cells))
	for _, c := range r.cells {
		if c.suppressed && !c.primary {
			rep.Secondary++
		}
		cell := Cell{Dim: c.key.dim, Group: c.key.group, Suppressed: c.suppressed}
		if c.key.period == periodWeek {
			cell.Period, cell.PeriodStart = Week, week
		} else {
			cell.Period, cell.PeriodStart = Day, week.AddDate(0, 0, c.key.period)
		}
		if !c.suppressed {
			cell.People, cell.Totals = len(c.people), c.totals
		}
		out = append(out, cell)
	}
	rep.Cells = len(out)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Dim != b.Dim {
			return a.Dim < b.Dim
		}
		if a.Period != b.Period {
			return a.Period < b.Period
		}
		if !a.PeriodStart.Equal(b.PeriodStart) {
			return a.PeriodStart.Before(b.PeriodStart)
		}
		return a.Group < b.Group
	})
	return out, rep
}

type release struct {
	cells []*relCell
	byKey map[cellKey]*relCell
	vars  []variable
	// cellVars maps a cell to the variables it sums.
	cellVars map[cellKey][]int
	// constraints are the per-day identities, as coefficient maps.
	constraints []map[int]uint64
}

func sigKey(teams []string) string { return strings.Join(teams, "\x00") }

// newRelease lays out the week's cells and the reader's unknowns. Every
// group that appears in the week gets a cell for every day and for the
// week, and every team with members gets them too, whether or not anyone
// contributed: an absent cell would say "nobody", which a withheld one
// does not.
func newRelease(in ReleaseInput, week time.Time) *release {
	r := &release{byKey: map[cellKey]*relCell{}, cellVars: map[cellKey][]int{}}
	teamsOf := func(m string) []string {
		t := append([]string(nil), in.Teams[m]...)
		sort.Strings(t)
		return slices.Compact(t)
	}
	cell := func(key cellKey) *relCell {
		c := r.byKey[key]
		if c == nil {
			c = &relCell{key: key, people: map[string]bool{}}
			r.byKey[key] = c
			r.cells = append(r.cells, c)
		}
		return c
	}
	varIdx := map[string]int{}
	getVar := func(v variable) int {
		name := fmt.Sprintf("%s|%d|%s|%s", v.dim, v.day, v.group, sigKey(v.teams))
		if i, ok := varIdx[name]; ok {
			return i
		}
		v.name, v.people = name, map[string]bool{}
		r.vars = append(r.vars, v)
		varIdx[name] = len(r.vars) - 1
		return len(r.vars) - 1
	}

	// Signatures: the members sharing one exact set of teams.
	sigMembers := map[string]map[string]bool{}
	sigTeams := map[string][]string{}
	sigOf := func(m string) string {
		ts := teamsOf(m)
		key := sigKey(ts)
		if sigMembers[key] == nil {
			sigMembers[key] = map[string]bool{}
			sigTeams[key] = ts
		}
		sigMembers[key][m] = true
		return key
	}
	members := make([]string, 0, len(in.Teams))
	for m := range in.Teams {
		members = append(members, m)
	}
	sort.Strings(members)
	for _, m := range members {
		sigOf(m)
	}

	type contrib struct {
		Contribution
		day int
		sig string
	}
	var cs []contrib
	repos, kinds := map[string]bool{}, map[string]bool{}
	// In a fixed order, so floating-point sums do not depend on the input's.
	ordered := append([]Contribution(nil), in.Contributions...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.Member != b.Member {
			return a.Member < b.Member
		}
		if !a.Day.Equal(b.Day) {
			return a.Day.Before(b.Day)
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		return a.Kind < b.Kind
	})
	for _, c := range ordered {
		d := int(Day.Start(c.Day).Sub(week).Hours() / 24)
		if d < 0 || d > 6 {
			continue
		}
		cs = append(cs, contrib{c, d, sigOf(c.Member)})
		repos[c.Repo], kinds[c.Kind] = true, true
	}
	sigs := sortedKeys(sigTeams)
	teams := map[string]bool{}
	for _, ts := range sigTeams {
		for _, t := range ts {
			teams[t] = true
		}
	}

	// Unknowns and cells, every day of the week for every group.
	for d := 0; d < 7; d++ {
		for _, g := range sortedKeys(repos) {
			i := getVar(variable{day: d, dim: ByRepo, group: g})
			for _, p := range []int{d, periodWeek} {
				key := cellKey{ByRepo, p, g}
				cell(key)
				r.cellVars[key] = append(r.cellVars[key], i)
			}
		}
		for _, g := range sortedKeys(kinds) {
			i := getVar(variable{day: d, dim: ByKind, group: g})
			for _, p := range []int{d, periodWeek} {
				key := cellKey{ByKind, p, g}
				cell(key)
				r.cellVars[key] = append(r.cellVars[key], i)
			}
		}
		for _, s := range sigs {
			i := getVar(variable{day: d, dim: ByTeam, teams: sigTeams[s]})
			for _, t := range sigTeams[s] {
				for _, p := range []int{d, periodWeek} {
					key := cellKey{ByTeam, p, t}
					r.cellVars[key] = append(r.cellVars[key], i)
				}
			}
		}
		for _, t := range sortedKeys(teams) {
			cell(cellKey{ByTeam, d, t})
			cell(cellKey{ByTeam, periodWeek, t})
		}
	}
	for _, c := range cs {
		for _, p := range []int{c.day, periodWeek} {
			for _, key := range []cellKey{{ByRepo, p, c.Repo}, {ByKind, p, c.Kind}} {
				cell(key).people[c.Member] = true
				cell(key).totals.Add(c.Totals)
			}
			for _, t := range sigTeams[c.sig] {
				cell(cellKey{ByTeam, p, t}).people[c.Member] = true
				cell(cellKey{ByTeam, p, t}).totals.Add(c.Totals)
			}
		}
		r.vars[getVar(variable{day: c.day, dim: ByRepo, group: c.Repo})].people[c.Member] = true
		r.vars[getVar(variable{day: c.day, dim: ByKind, group: c.Kind})].people[c.Member] = true
		r.vars[getVar(variable{day: c.day, dim: ByTeam, teams: sigTeams[c.sig]})].people[c.Member] = true
	}
	// A signature nobody with it worked under that day: learning that is
	// learning about them, so it counts as about them. (A repository or
	// kind nobody worked in is about nobody.)
	for i, v := range r.vars {
		if v.dim == ByTeam && len(v.people) == 0 {
			for m := range sigMembers[sigKey(v.teams)] {
				r.vars[i].people[m] = true
			}
		}
	}
	// Per day: repositories, kinds and signatures each sum to the total.
	for d := 0; d < 7; d++ {
		repoMinusKind, repoMinusSig := map[int]uint64{}, map[int]uint64{}
		for i, v := range r.vars {
			if v.day != d {
				continue
			}
			switch v.dim {
			case ByRepo:
				repoMinusKind[i], repoMinusSig[i] = 1, 1
			case ByKind:
				repoMinusKind[i] = gfNeg(1)
			case ByTeam:
				repoMinusSig[i] = gfNeg(1)
			}
		}
		r.constraints = append(r.constraints, repoMinusKind, repoMinusSig)
	}
	sort.SliceStable(r.cells, func(i, j int) bool { return lessKey(r.cells[i].key, r.cells[j].key) })
	return r
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func lessKey(a, b cellKey) bool {
	if a.dim != b.dim {
		return a.dim < b.dim
	}
	if a.period != b.period {
		return a.period < b.period
	}
	return a.group < b.group
}

// smaller orders cells for secondary suppression: fewest people, then
// least work, then by key, so the release is deterministic.
func smaller(a, b *relCell) bool {
	if len(a.people) != len(b.people) {
		return len(a.people) < len(b.people)
	}
	if a.totals.Instructions != b.totals.Instructions {
		return a.totals.Instructions < b.totals.Instructions
	}
	return lessKey(a.key, b.key)
}

func union(cells []*relCell) int {
	u := map[string]bool{}
	for _, c := range cells {
		for m := range c.people {
			u[m] = true
		}
	}
	return len(u)
}

// linePass is the classic complementary suppression, run first because it
// settles most weeks cheaply: in every line — the groups of one dimension
// on one day or week, and one group's days and week — the withheld cells
// together must cover nobody or at least k people, or the smallest visible
// sibling is withheld too: a total minus the visible cells is the withheld
// cells' sum. The exact check (secure) has the last word.
func (r *release) linePass(k int) {
	for changed := true; changed; {
		changed = false
		lines := map[string][]*relCell{}
		for _, c := range r.cells {
			lines[fmt.Sprintf("p|%s|%d", c.key.dim, c.key.period)] = append(lines[fmt.Sprintf("p|%s|%d", c.key.dim, c.key.period)], c)
			lines["g|"+string(c.key.dim)+"|"+c.key.group] = append(lines["g|"+string(c.key.dim)+"|"+c.key.group], c)
		}
		keys := make([]string, 0, len(lines))
		for key := range lines {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			line := lines[key]
			var hidden, shown []*relCell
			for _, c := range line {
				if c.suppressed {
					hidden = append(hidden, c)
				} else {
					shown = append(shown, c)
				}
			}
			// What the line gives away is the withheld cells' sum: fine when
			// it is about nobody or about at least k people.
			if u := union(hidden); len(shown) == 0 || u == 0 || u >= k {
				continue
			}
			// In a group's own line, withhold a day before the week: the
			// week then still shows.
			pick := shown[0]
			for _, c := range shown[1:] {
				if (pick.key.period == periodWeek) != (c.key.period == periodWeek) {
					if pick.key.period == periodWeek {
						pick = c
					}
					continue
				}
				if smaller(c, pick) {
					pick = c
				}
			}
			pick.suppressed = true
			changed = true
		}
	}
}

// secure runs the exact check, withholding cells until it passes. If the
// check would exceed its budget it withholds every day cell and checks
// again, and failing that withholds the whole week. It returns the
// fallback taken, if any, and how many groups it checked.
func (r *release) secure(k int) (string, int) {
	ck, ok := r.newChecker(k)
	if ok {
		if done, n := r.converge(ck); done {
			return "", n
		}
	}
	checks := ck.checks
	for _, c := range r.cells {
		if c.key.period != periodWeek {
			c.suppressed = true
		}
	}
	ck, ok = r.newChecker(k)
	if ok {
		done, n := r.converge(ck)
		checks += n
		if done {
			return "days", checks
		}
	}
	for _, c := range r.cells {
		c.suppressed = true
	}
	return "all", checks
}

// converge withholds cells until no group's check finds a disclosure. It
// reports false when the budget ran out.
//
// Withholding a cell only removes what the reader knows, so a group found
// safe stays safe: each round resumes where the last one stopped instead
// of starting over.
func (r *release) converge(ck *checker) (bool, int) {
	pos := 0
	for round := 0; round < maxSecondaryRounds; round++ {
		bad, next, ok := ck.find(r, pos)
		if !ok {
			return false, ck.checks
		}
		if bad == nil {
			return true, ck.checks
		}
		pos = next
		c := r.complement(bad)
		if c == nil {
			return false, ck.checks
		}
		c.suppressed = true
	}
	return false, ck.checks
}

// complement picks the cell to withhold against a disclosure involving
// the variables bad: the smallest visible cell that sums one of them or
// shares a day with one, else the smallest visible cell of the week.
func (r *release) complement(bad []int) *relCell {
	badSet := map[int]bool{}
	days := map[int]bool{}
	for _, v := range bad {
		badSet[v] = true
		days[r.vars[v].day] = true
	}
	var near, any *relCell
	for _, c := range r.cells {
		if c.suppressed {
			continue
		}
		if any == nil || smaller(c, any) {
			any = c
		}
		touches := days[c.key.period]
		for _, v := range r.cellVars[c.key] {
			if badSet[v] {
				touches = true
				break
			}
		}
		if touches && (near == nil || smaller(c, near)) {
			near = c
		}
	}
	if near != nil {
		return near
	}
	return any
}

// checker holds what the exact check reuses across rounds: the groups of
// fewer than k people to examine, each as the set of unknowns that only
// its members contributed to, and the identities' complement.
type checker struct {
	sets  [][]int
	empty []int
	nuZ   [][]uint64
	proj  *projection
	work  int
	// checks counts groups examined.
	checks int
}

func (r *release) newChecker(k int) (*checker, bool) {
	ck := &checker{}
	// Small unknowns: fewer than k people contributed to each. Those
	// nobody contributed to (a repository or kind on a day without work)
	// belong to every group's set: they add no one.
	var small []int
	for i, v := range r.vars {
		if len(v.people) < k {
			small = append(small, i)
		}
		if len(v.people) == 0 {
			ck.empty = append(ck.empty, i)
		}
	}
	groups, ok := maximalGroups(r.vars, small, k-1)
	if !ok {
		return ck, false
	}
	widest := len(ck.empty)
	for _, g := range groups {
		var set []int
		for _, i := range small {
			if subset(r.vars[i].people, g) {
				set = append(set, i)
			}
		}
		ck.sets = append(ck.sets, set)
		widest = max(widest, len(set))
	}
	ck.proj = newProjection(widest)
	var structural [][]uint64
	for _, c := range r.constraints {
		structural = append(structural, denseRow(c, len(r.vars)))
	}
	ck.nuZ = ck.proj.apply(gfComplement(structural, len(r.vars)))
	return ck, true
}

func denseRow(m map[int]uint64, n int) []uint64 {
	v := make([]uint64, n)
	for i, c := range m {
		v[i] = c
	}
	return v
}

// find examines groups from position pos on and returns the first one
// the visible cells disclose something about (its unknowns, and its
// position), nil when there is none, and false when the budget ran out.
func (ck *checker) find(r *release, pos int) ([]int, int, bool) {
	n := len(r.vars)
	if n == 0 || len(ck.sets) == 0 {
		return nil, pos, true
	}
	var known [][]uint64
	for _, c := range r.constraints {
		known = append(known, denseRow(c, n))
	}
	for _, c := range r.cells {
		if c.suppressed {
			continue
		}
		m := map[int]uint64{}
		for _, v := range r.cellVars[c.key] {
			m[v] = 1
		}
		known = append(known, denseRow(m, n))
	}
	ck.work += len(known) * len(known) * n
	nuF := ck.proj.apply(gfComplement(known, n))
	// What the cells tell about nobody — that a repository was idle on
	// some day — is the baseline every group is compared with.
	base := gap(ck.empty, nuF, ck.nuZ)
	for ; pos < len(ck.sets); pos++ {
		set := ck.sets[pos]
		ck.work += 2 * len(set) * len(set) * (len(nuF[0]) + 1)
		ck.checks++
		if ck.work > maxCheckWork {
			return nil, pos, false
		}
		if gap(set, nuF, ck.nuZ) > base {
			return set, pos, true
		}
	}
	return nil, pos, true
}

// gap is how many independent informative facts the known equations give
// about the unknowns s alone: dim(F ∩ E_s) - dim(Z ∩ E_s), where F is
// everything the reader knows, Z the identities that hold whatever the
// figures, and E_s the combinations of s. By the modular law, a group's
// set s containing the idle set e discloses something about the group
// exactly when gap(s) > gap(e).
func gap(s []int, nuF, nuZ [][]uint64) int {
	pick := func(nu [][]uint64) [][]uint64 {
		out := make([][]uint64, len(s))
		for i, j := range s {
			out[i] = nu[j]
		}
		return out
	}
	// dim(F ∩ E_s) = |s| - rank(nuF_s); likewise for Z.
	return gfRank(pick(nuZ)) - gfRank(pick(nuF))
}

// projection maps complement coordinates to at most width random
// combinations of them. Whether at most width vectors are independent is
// all the check asks; a random projection to width dimensions preserves
// independence except with probability below width/2^61, and dependence
// always. The seed is fixed, so a release is reproducible; the matrices it
// is applied to come from the cells' shape, which an uploader cannot steer
// toward it.
type projection struct {
	width int
	state uint64
	cache map[int][][]uint64
}

func newProjection(width int) *projection {
	return &projection{width: max(width, 1), state: 0x7465616d706c616e, cache: map[int][][]uint64{}}
}

func (p *projection) next() uint64 {
	// splitmix64
	p.state += 0x9e3779b97f4a7c15
	z := p.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return gfReduce((z ^ (z >> 31)) & gfP)
}

// matrix is the dim x width projection for vectors of length dim.
func (p *projection) matrix(dim int) [][]uint64 {
	if m, ok := p.cache[dim]; ok {
		return m
	}
	m := make([][]uint64, dim)
	for i := range m {
		m[i] = make([]uint64, p.width)
		for j := range m[i] {
			m[i][j] = p.next()
		}
	}
	p.cache[dim] = m
	return m
}

func (p *projection) apply(nu [][]uint64) [][]uint64 {
	if len(nu) == 0 || len(nu[0]) <= p.width {
		return nu
	}
	m := p.matrix(len(nu[0]))
	out := make([][]uint64, len(nu))
	for i, v := range nu {
		w := make([]uint64, p.width)
		for a, x := range v {
			if x == 0 {
				continue
			}
			row := m[a]
			for j := range w {
				w[j] = gfAdd(w[j], gfMul(x, row[j]))
			}
		}
		out[i] = w
	}
	return out
}

func subset(people map[string]bool, group map[string]bool) bool {
	for m := range people {
		if !group[m] {
			return false
		}
	}
	return true
}

// maximalGroups enumerates the maximal unions of small variables' people
// that have at most size members. Checking those suffices: a disclosure
// about a group is one about every group containing it.
func maximalGroups(vars []variable, small []int, size int) ([]map[string]bool, bool) {
	type group struct {
		members map[string]bool
		key     string
	}
	keyOf := func(m map[string]bool) string {
		s := make([]string, 0, len(m))
		for x := range m {
			s = append(s, x)
		}
		sort.Strings(s)
		return strings.Join(s, "\x00")
	}
	seen := map[string]bool{}
	frontier := []group{{members: map[string]bool{}, key: ""}}
	var maximal []map[string]bool
	for len(frontier) > 0 {
		var next []group
		for _, g := range frontier {
			extended := false
			for _, i := range small {
				if subset(vars[i].people, g.members) {
					continue
				}
				u := map[string]bool{}
				for m := range g.members {
					u[m] = true
				}
				for m := range vars[i].people {
					u[m] = true
				}
				if len(u) > size {
					continue
				}
				extended = true
				key := keyOf(u)
				if seen[key] {
					continue
				}
				seen[key] = true
				if len(seen) > maxCandidateGroups {
					return nil, false
				}
				next = append(next, group{members: u, key: key})
			}
			if !extended && len(g.members) > 0 {
				maximal = append(maximal, g.members)
			}
		}
		frontier = next
	}
	return maximal, true
}
