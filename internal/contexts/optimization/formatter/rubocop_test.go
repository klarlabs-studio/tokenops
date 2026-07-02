package formatter

import (
	"strings"
	"testing"
)

const rubocopRaw = `Inspecting 12 files
..C..W...E..

Offenses:

app/models/user.rb:10:5: C: Style/StringLiterals: Prefer single-quoted strings when you don't need string interpolation.
    name = "Alice"
           ^^^^^^^
app/controllers/users_controller.rb:22:1: W: Lint/UselessAssignment: Useless assignment to variable - ` + "`x`" + `.
    x = compute
    ^
app/services/report.rb:5:3: E: Lint/Syntax: unexpected token kEND
    def
    ^^^

12 files inspected, 3 offenses detected
`

func TestRubocop_FindingsSurviveEveryLevel(t *testing.T) {
	r := NewRubocop()
	critical := []string{
		"app/models/user.rb:10:5: C: Style/StringLiterals: Prefer single-quoted strings when you don't need string interpolation.",
		"app/controllers/users_controller.rb:22:1: W: Lint/UselessAssignment: Useless assignment to variable - `x`.",
		"app/services/report.rb:5:3: E: Lint/Syntax: unexpected token kEND",
		"12 files inspected, 3 offenses detected",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := r.Format([]byte(rubocopRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if !res.CriticalKept {
			t.Fatalf("level=%s CriticalKept=false", level)
		}
		compact := string(res.Compact)
		for _, c := range critical {
			if !strings.Contains(compact, c) {
				t.Errorf("level=%s dropped critical %q\ngot:\n%s", level, c, compact)
			}
		}
	}
}

func TestRubocop_BalancedDropsNoise(t *testing.T) {
	r := NewRubocop()
	res, _ := r.Format([]byte(rubocopRaw), LossBalanced)
	compact := string(res.Compact)
	// Inspection banner, progress line, and the source snippet + caret echoes
	// under each finding must be gone.
	for _, noise := range []string{
		"Inspecting 12 files",
		"..C..W...E..",
		`name = "Alice"`,
		"^^^^^^^",
		"x = compute",
		"def",
		"^^^",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// Findings, the Offenses header, and the summary survive.
	if !strings.Contains(compact, "Offenses:") {
		t.Errorf("balanced dropped the Offenses header:\n%s", compact)
	}
	if !strings.Contains(compact, "Style/StringLiterals") {
		t.Errorf("balanced dropped a finding:\n%s", compact)
	}
	if !strings.Contains(compact, "3 offenses detected") {
		t.Errorf("balanced dropped the summary:\n%s", compact)
	}
}

func TestRubocop_MonotonicReduction(t *testing.T) {
	r := NewRubocop()
	last := 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := r.Format([]byte(rubocopRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestRubocop_NonMatchingFallsBackToGeneric(t *testing.T) {
	r := NewRubocop()
	raw := "added 214 packages in 3s\nfound 0 vulnerabilities\n"
	res, ok := r.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestRubocop_SourceEchoNotCritical(t *testing.T) {
	r := NewRubocop()
	if r.CriticalLine("    name = \"Alice\"") {
		t.Error("source snippet should not be critical")
	}
	if r.CriticalLine("           ^^^^^^^") {
		t.Error("caret echo should not be critical")
	}
	if !r.CriticalLine("app/models/user.rb:10:5: C: Style/StringLiterals: x") {
		t.Error("finding line should be critical")
	}
	if !r.CriticalLine("12 files inspected, 3 offenses detected") {
		t.Error("summary line should be critical")
	}
}
