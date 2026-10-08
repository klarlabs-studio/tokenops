package team

import (
	"fmt"
	"testing"
	"time"
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

func TestReleaseScales(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	for _, k := range []int{3, 5} {
		start := time.Now()
		cells, rep := Release(bigWeek(k))
		took := time.Since(start)
		hidden := 0
		for _, c := range cells {
			if c.Suppressed {
				hidden++
			}
		}
		t.Logf("k=%d: %d cells, %d withheld (%d primary, %d secondary), %d checks, fallback %q, %s",
			k, rep.Cells, hidden, rep.Primary, rep.Secondary, rep.Checks, rep.Fallback, took)
		if took > 30*time.Second {
			t.Errorf("k=%d took %s", k, took)
		}
		in := bigWeek(k)
		assertNoResidualLeak(t, in, cells)
	}
}
