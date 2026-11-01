package eval

import (
	"strings"
	"testing"
	"testing/fstest"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/formatter"
)

// stubFormatter compacts by keeping only the lines that contain keep, so a
// test controls exactly which evidence survives. Lines containing critical
// are its critical lines.
type stubFormatter struct {
	command  string
	keep     []string
	critical string
	rewrite  func(string) string
}

func (s stubFormatter) Command() string { return s.command }

func (s stubFormatter) CriticalLine(line string) bool {
	return s.critical != "" && strings.Contains(line, s.critical)
}

func (s stubFormatter) Format(raw []byte, _ formatter.LossLevel) (formatter.Result, bool) {
	var kept []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		for _, k := range s.keep {
			if strings.Contains(line, k) {
				if s.rewrite != nil {
					line = s.rewrite(line)
				}
				kept = append(kept, line)
				break
			}
		}
	}
	compact := strings.Join(kept, "\n")
	return formatter.Result{
		Compact: []byte(compact), BytesBefore: len(raw), BytesAfter: len(compact), CriticalKept: true,
	}, true
}

const stubRaw = "noise line one\nFAIL TestLogin\nsrc/a.go:12:3: undefined: x\nnoise line two\n3 failed, 9 passed\n"

func TestEvaluateFmtRecallScoresEachItem(t *testing.T) {
	cases := []RecallCase{{
		ID: "stub.run", Command: "stub", Output: stubRaw,
		Evidence: []Evidence{
			{Kind: EvidenceFailingTest, Text: "TestLogin"},
			{Kind: EvidenceErrorLocation, Text: "src/a.go:12:3"},
			{Kind: EvidenceCount, Text: "3 failed"},
		},
	}}
	tests := []struct {
		name          string
		f             stubFormatter
		wantRecalled  int
		wantCritical  int
		wantCritTotal int
		wantMissed    []string
		wantMatch     map[string]MatchKind
	}{
		{
			name:         "everything kept",
			f:            stubFormatter{command: "stub", keep: []string{"FAIL", "src/", "failed"}},
			wantRecalled: 3, wantCritical: 2, wantCritTotal: 2,
			wantMatch: map[string]MatchKind{"TestLogin": MatchExact, "3 failed": MatchExact},
		},
		{
			name:         "the count dropped",
			f:            stubFormatter{command: "stub", keep: []string{"FAIL", "src/"}},
			wantRecalled: 2, wantCritical: 2, wantCritTotal: 2,
			wantMissed: []string{"3 failed"},
		},
		{
			name:         "a failing test dropped",
			f:            stubFormatter{command: "stub", keep: []string{"src/", "failed"}},
			wantRecalled: 2, wantCritical: 1, wantCritTotal: 2,
			wantMissed: []string{"TestLogin"},
		},
		{
			name: "whitespace rewritten still matches, normalized",
			f: stubFormatter{command: "stub", keep: []string{"FAIL", "src/", "failed"},
				rewrite: func(l string) string { return strings.ReplaceAll(l, " ", "   ") }},
			wantRecalled: 3, wantCritical: 2, wantCritTotal: 2,
			wantMatch: map[string]MatchKind{"3 failed": MatchNormalized, "TestLogin": MatchExact},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev := EvaluateFmtRecall(cases, []formatter.Formatter{tc.f}, []formatter.LossLevel{formatter.LossBalanced})
			if len(ev.Levels) != 1 {
				t.Fatalf("levels = %d, want 1", len(ev.Levels))
			}
			lv := ev.Levels[0]
			got := lv.Overall
			if got.Recalled != tc.wantRecalled || got.CriticalRecalled != tc.wantCritical || got.CriticalItems != tc.wantCritTotal {
				t.Fatalf("overall = %+v, want recalled %d critical %d/%d", got, tc.wantRecalled, tc.wantCritical, tc.wantCritTotal)
			}
			var missed []string
			for _, m := range lv.Misses {
				missed = append(missed, m.Text)
			}
			if strings.Join(missed, ",") != strings.Join(tc.wantMissed, ",") {
				t.Errorf("misses = %v, want %v", missed, tc.wantMissed)
			}
			for _, it := range lv.Cases[0].Items {
				if want, ok := tc.wantMatch[it.Text]; ok && it.Match != want {
					t.Errorf("%q matched %q, want %q", it.Text, it.Match, want)
				}
			}
		})
	}
}

func TestEvaluateFmtRecallMeasuresSavings(t *testing.T) {
	cases := []RecallCase{{ID: "stub.run", Command: "stub", Output: stubRaw,
		Evidence: []Evidence{{Kind: EvidenceFailingTest, Text: "TestLogin"}}}}
	f := stubFormatter{command: "stub", keep: []string{"FAIL"}}
	ev := EvaluateFmtRecall(cases, []formatter.Formatter{f}, []formatter.LossLevel{formatter.LossBalanced})
	o := ev.Levels[0].Overall
	if o.BytesBefore != len(stubRaw) || o.BytesAfter != len("FAIL TestLogin") {
		t.Fatalf("bytes = %d -> %d", o.BytesBefore, o.BytesAfter)
	}
	if o.TokensBefore <= o.TokensAfter || o.TokensAfter == 0 {
		t.Errorf("tokens = %d -> %d, want a positive saving", o.TokensBefore, o.TokensAfter)
	}
	if s := o.SavedPct(); s < 50 || s > 100 {
		t.Errorf("saved = %.1f%%", s)
	}
	if r := o.Recall(); r != 1 {
		t.Errorf("recall = %v, want 1", r)
	}
}

func TestEvaluateFmtRecallGroupsByFormatterAndFallsBackToGeneric(t *testing.T) {
	cases := []RecallCase{
		{ID: "stub.a", Command: "stub", Output: stubRaw, Evidence: []Evidence{{Kind: EvidenceFailingTest, Text: "TestLogin"}}},
		{ID: "stub.b", Command: "stub", Output: stubRaw, Evidence: []Evidence{{Kind: EvidenceCount, Text: "3 failed"}}},
		{ID: "rg.search", Command: "rg", Output: "src/a.go:12:needle\n", Evidence: []Evidence{{Kind: EvidenceMatch, Text: "src/a.go:12"}}},
	}
	f := stubFormatter{command: "stub", keep: []string{"FAIL"}}
	ev := EvaluateFmtRecall(cases, []formatter.Formatter{f}, []formatter.LossLevel{formatter.LossConservative})
	by := map[string]FormatterRecall{}
	for _, fr := range ev.Levels[0].Formatters {
		by[fr.Formatter] = fr
	}
	if by["stub"].Cases != 2 || by["stub"].Items != 2 || by["stub"].Recalled != 1 {
		t.Errorf("stub = %+v", by["stub"])
	}
	if g := by[GenericFormatter]; g.Cases != 1 || g.Recalled != 1 {
		t.Errorf("generic = %+v (formatters %+v)", g, ev.Levels[0].Formatters)
	}
}

func TestEvidenceCriticality(t *testing.T) {
	no := false
	tests := []struct {
		e    Evidence
		want bool
	}{
		{Evidence{Kind: EvidenceFailingTest}, true},
		{Evidence{Kind: EvidenceErrorLocation}, true},
		{Evidence{Kind: EvidenceErrorMessage}, true},
		{Evidence{Kind: EvidenceChangedPath}, true},
		{Evidence{Kind: EvidenceExitStatus}, true},
		{Evidence{Kind: EvidenceFailedResource}, true},
		{Evidence{Kind: EvidenceCount}, false},
		{Evidence{Kind: EvidenceMatch}, false},
		{Evidence{Kind: EvidenceInstalled}, false},
		{Evidence{Kind: EvidenceFailingTest, Critical: &no}, false},
	}
	for _, tc := range tests {
		if got := tc.e.IsCritical(); got != tc.want {
			t.Errorf("%+v critical = %v, want %v", tc.e, got, tc.want)
		}
	}
}

func TestEvaluateFmtRecallReportsTheGuard(t *testing.T) {
	cases := []RecallCase{{ID: "stub.run", Command: "stub", Output: stubRaw,
		Evidence: []Evidence{{Kind: EvidenceFailingTest, Text: "TestLogin"}}}}
	// The line holding the item is one the formatter calls critical.
	f := stubFormatter{command: "stub", keep: []string{"FAIL"}, critical: "FAIL"}
	ev := EvaluateFmtRecall(cases, []formatter.Formatter{f}, []formatter.LossLevel{formatter.LossBalanced})
	if it := ev.Levels[0].Cases[0].Items[0]; !it.Guarded {
		t.Errorf("item on a critical line should be reported guarded: %+v", it)
	}
}

func TestLoadRecallCases(t *testing.T) {
	fsys := fstest.MapFS{
		"c/git.status.txt":  {Data: []byte("\tmodified:   a.go\n")},
		"c/git.status.json": {Data: []byte(`{"description":"d","evidence":[{"kind":"changed_path","text":"a.go"}]}`)},
		"c/rg.search.txt":   {Data: []byte("x.go:1:y\n")},
		"c/rg.search.json":  {Data: []byte(`{"command":"rg","evidence":[{"kind":"match","text":"x.go:1"}]}`)},
	}
	cases, err := LoadRecallCases(fsys, "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 || cases[0].ID != "git.status" || cases[0].Command != "git" || cases[1].Command != "rg" {
		t.Fatalf("cases = %+v", cases)
	}

	bad := []struct {
		name string
		fsys fstest.MapFS
		want string
	}{
		{"no output", fstest.MapFS{"c/x.y.json": {Data: []byte(`{"evidence":[{"kind":"count","text":"1"}]}`)}}, "x.y.txt"},
		{"no evidence", fstest.MapFS{"c/x.y.json": {Data: []byte(`{}`)}, "c/x.y.txt": {Data: []byte("1")}}, "no evidence"},
		{"label not in output", fstest.MapFS{"c/x.y.json": {Data: []byte(`{"evidence":[{"kind":"count","text":"9"}]}`)}, "c/x.y.txt": {Data: []byte("1")}}, "not in the raw output"},
		{"unknown kind", fstest.MapFS{"c/x.y.json": {Data: []byte(`{"evidence":[{"kind":"vibe","text":"1"}]}`)}, "c/x.y.txt": {Data: []byte("1")}}, "unknown kind"},
		{"empty dir", fstest.MapFS{"c/readme": {Data: []byte("x")}}, "no labeled"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadRecallCases(tc.fsys, "c")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
