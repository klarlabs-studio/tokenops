package eval

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/formatter"
)

// The labeled corpus is the regression gate for "what fmt never drops":
// every critical evidence item must survive every built-in formatter at
// every loss level. A miss here is a formatter whose CriticalLine predicate
// does not cover a line an agent needs — a gap enforceCritical cannot see,
// because the guard only protects lines the formatter calls critical.
func TestBundledCorpusKeepsEveryCriticalItem(t *testing.T) {
	cases, err := BundledRecallCases()
	if err != nil {
		t.Fatalf("BundledRecallCases: %v", err)
	}
	ev := EvaluateFmtRecall(cases, formatter.DefaultFormatters(), AllLevels)
	for _, m := range ev.CriticalMisses() {
		t.Errorf("%s (%s) dropped critical %s %q", m.Case, m.Formatter, m.Kind, m.Text)
	}
	for _, lv := range ev.Levels {
		o := lv.Overall
		t.Logf("%-12s cases %d  recall %.3f (%d/%d)  critical %d/%d  saved %.1f%% bytes, %d tokens  fell back %d",
			lv.Level, o.Cases, o.Recall(), o.Recalled, o.Items, o.CriticalRecalled, o.CriticalItems, o.SavedPct(), o.SavedTokens(), o.FellBack)
		for _, m := range lv.Misses {
			if !m.Critical {
				t.Logf("  %s: non-critical miss in %s: %s %q", lv.Level, m.Case, m.Kind, m.Text)
			}
		}
	}
}

// Every built-in formatter is judged on at least one labeled output, so a
// formatter added to DefaultFormatters without a fixture fails here.
func TestBundledCorpusCoversEveryFormatter(t *testing.T) {
	cases, err := BundledRecallCases()
	if err != nil {
		t.Fatal(err)
	}
	ev := EvaluateFmtRecall(cases, formatter.DefaultFormatters(), []formatter.LossLevel{formatter.LossBalanced})
	judged := map[string]bool{}
	for _, fr := range ev.Levels[0].Formatters {
		judged[fr.Formatter] = true
	}
	for _, f := range formatter.DefaultFormatters() {
		if !judged[f.Command()] {
			t.Errorf("formatter %q has no labeled output under %s", f.Command(), RecallCorpusDir)
		}
	}
	if !judged[GenericFormatter] {
		t.Errorf("no labeled output exercises the generic scrub")
	}
}

// The levels trade recall for savings in one direction only: a lossier
// level never saves less, and never recovers more, than a gentler one.
func TestBundledCorpusLevelsAreMonotonic(t *testing.T) {
	cases, err := BundledRecallCases()
	if err != nil {
		t.Fatal(err)
	}
	ev := EvaluateFmtRecall(cases, formatter.DefaultFormatters(), AllLevels)
	for i := 1; i < len(ev.Levels); i++ {
		prev, cur := ev.Levels[i-1].Overall, ev.Levels[i].Overall
		if cur.BytesAfter > prev.BytesAfter {
			t.Errorf("%s output (%d B) larger than %s (%d B)", ev.Levels[i].Level, cur.BytesAfter, ev.Levels[i-1].Level, prev.BytesAfter)
		}
		if cur.Recalled > prev.Recalled {
			t.Errorf("%s recalled %d items, more than %s (%d)", ev.Levels[i].Level, cur.Recalled, ev.Levels[i-1].Level, prev.Recalled)
		}
	}
	if c := ev.Levels[0].Overall; c.Recall() != 1 {
		t.Errorf("conservative recall = %.3f; it drops noise only and must recover every item", c.Recall())
	}
}
