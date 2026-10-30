package uniteconomics

import "testing"

// The units the team upload files by day and repository are the report's
// own: the same commits and pull requests, with the same value placed on
// each, and nothing from the baseline.
func TestPlacedUnitsAreTheReportsUnits(t *testing.T) {
	in := fixture()
	placed := in.PlacedUnits()
	r := Build(in)
	got := map[string]struct {
		n   int
		usd float64
	}{}
	for _, p := range placed {
		if p.Root != "/w/app" || p.At.Before(in.Since) || !p.At.Before(in.Until) {
			t.Errorf("placed %+v outside the period or repository", p)
		}
		g := got[p.Unit]
		g.n++
		g.usd += p.APIEquivalentUSD
		got[p.Unit] = g
	}
	if c := got[UnitCommit]; c.n != r.Commits.Current.Units || c.usd != r.Commits.Current.APIEquivalentUSD {
		t.Errorf("commits %+v, report %+v", c, r.Commits.Current)
	}
	if p := got[UnitMergedPR]; p.n != r.PRs.Current.Units || p.usd != r.PRs.Current.APIEquivalentUSD {
		t.Errorf("merged PRs %+v, report %+v", p, r.PRs.Current)
	}
	for i := 1; i < len(placed); i++ {
		if placed[i].At.Before(placed[i-1].At) {
			t.Fatal("not oldest first")
		}
	}
}
