package team

import (
	"fmt"
	"testing"
)

// bigWeek is a 60-person organisation: six teams (some people in two),
// twenty repositories, every kind of work, weekdays and a little weekend.
func bigWeek(k int) ReleaseInput {
	in := ReleaseInput{WeekStart: testWeek, MinGroup: k, Teams: map[string][]string{}}
	kinds := []string{"unknown", "lookup", "research", "edit", "deep"}
	for i := 0; i < 60; i++ {
		m := fmt.Sprintf("m%02d", i)
		teams := []string{fmt.Sprintf("team%d", i%6)}
		if i%10 == 0 {
			teams = append(teams, fmt.Sprintf("team%d", (i/10+1)%6))
		}
		in.Teams[m] = teams
		for d := 0; d < 7; d++ {
			if d >= 5 && (i+d)%7 != 0 {
				continue
			}
			for j := 0; j < 2; j++ {
				repo := fmt.Sprintf("acme/r%02d", (i*3+d+j*7)%20)
				in.Contributions = append(in.Contributions, work(m, d, repo, kinds[(i+d+j)%5], int64(1+(i*d+j)%9)))
			}
		}
	}
	return in
}

// A realistic organisation is released without falling back and with
// most of its cells shown. (No wall-clock bound: CI machines vary; see
// BenchmarkRelease for timings.)
func TestReleaseBigOrganisation(t *testing.T) {
	in := bigWeek(3)
	cells, rep := Release(in)
	hidden := 0
	for _, c := range cells {
		if c.Suppressed {
			hidden++
		}
	}
	if rep.Fallback != "" || hidden*2 > len(cells) {
		t.Errorf("%d of %d withheld, report %+v", hidden, len(cells), rep)
	}
	assertNoResidualLeak(t, in, cells)
}

// go test -run '^$' -bench Release ./internal/contexts/team
// (Apple M-series: about 30 ms at k = 3, about 2 s at k = 5.)
func BenchmarkRelease(b *testing.B) {
	for _, k := range []int{3, 5} {
		in := bigWeek(k)
		b.Run(fmt.Sprintf("k=%d", k), func(b *testing.B) {
			for b.Loop() {
				Release(in)
			}
		})
	}
}
