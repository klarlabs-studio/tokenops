package fmtlearn

import (
	"testing"
	"time"
)

func compress(id, cmd string, raw, saved int64, handled, generic bool) Record {
	return Record{
		Type: RecordCompress, ID: id, Command: cmd, RawBytes: raw,
		TokensSaved: saved, Handled: handled, GenericFallback: generic,
		CriticalKept: true, TS: time.Unix(0, 0),
	}
}

func access(id, cmd string) Record {
	return Record{Type: RecordAccess, ID: id, Command: cmd, TS: time.Unix(0, 0)}
}

func TestAnalyze_NextFormattersRankedByRawBytes(t *testing.T) {
	recs := make([]Record, 0, 18)
	// "make": 6 generic runs (no dedicated formatter), large raw -> top candidate.
	for i := range 6 {
		recs = append(recs, compress("m"+string(rune('0'+i)), "make", 5000, 100, false, true))
	}
	// "jq": 6 generic runs, small raw -> lower candidate.
	for i := range 6 {
		recs = append(recs, compress("j"+string(rune('0'+i)), "jq", 200, 5, false, true))
	}
	// "go": 6 handled runs -> NOT a candidate.
	for i := range 6 {
		recs = append(recs, compress("g"+string(rune('0'+i)), "go", 9000, 3000, true, false))
	}
	rep := Analyze(recs, Thresholds{})
	if len(rep.NextFormatters) != 2 {
		t.Fatalf("want 2 next-formatter candidates, got %d: %+v", len(rep.NextFormatters), rep.NextFormatters)
	}
	if rep.NextFormatters[0].Command != "make" {
		t.Errorf("expected make ranked first (most raw bytes), got %s", rep.NextFormatters[0].Command)
	}
	// "go" must not be a candidate (it has a dedicated formatter).
	for _, c := range rep.NextFormatters {
		if c.Command == "go" {
			t.Error("go has a formatter and must not be a next-formatter candidate")
		}
	}
}

func TestAnalyze_CriticalMissFromReAccess(t *testing.T) {
	recs := make([]Record, 0, 13)
	// docker: 10 handled runs, 3 re-accessed -> access rate 0.3 > ceiling.
	for i := range 10 {
		recs = append(recs, compress("d"+string(rune('0'+i)), "docker", 1000, 300, true, false))
	}
	recs = append(recs, access("d0", "docker"), access("d1", "docker"), access("d2", "docker"))
	rep := Analyze(recs, Thresholds{})
	if len(rep.CriticalMisses) != 1 || rep.CriticalMisses[0].Command != "docker" {
		t.Fatalf("expected docker flagged as critical-miss, got %+v", rep.CriticalMisses)
	}
	if rep.CriticalMisses[0].AccessRate < 0.29 {
		t.Errorf("access rate = %.2f, want ~0.30", rep.CriticalMisses[0].AccessRate)
	}
	// A level hint should recommend lowering docker's level.
	var found bool
	for _, h := range rep.LevelHints {
		if h.Command == "docker" && h.Suggestion == "lower" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a 'lower' level hint for docker, got %+v", rep.LevelHints)
	}
}

func TestAnalyze_AccessAttributedByID(t *testing.T) {
	// An access row with no Command but a known compress ID must attribute
	// to that compression's command.
	recs := make([]Record, 0, 7)
	recs = append(recs,
		compress("x1", "terraform", 2000, 500, true, false),
		Record{Type: RecordAccess, ID: "x1", TS: time.Unix(0, 0)}, // no Command field
	)
	// Bump runs to clear MinRuns so the stat surfaces in Commands anyway.
	for i := range 5 {
		recs = append(recs, compress("t"+string(rune('0'+i)), "terraform", 2000, 500, true, false))
	}
	rep := Analyze(recs, Thresholds{})
	var tf *CommandStat
	for i := range rep.Commands {
		if rep.Commands[i].Command == "terraform" {
			tf = &rep.Commands[i]
		}
	}
	if tf == nil || tf.Accesses != 1 {
		t.Fatalf("access not attributed via id; terraform stat = %+v", tf)
	}
}

func TestAnalyze_RaiseHintWhenNeverReaccessed(t *testing.T) {
	recs := make([]Record, 0, 20)
	for i := range 20 {
		recs = append(recs, compress("g"+string(rune('a'+i)), "git", 800, 300, true, false))
	}
	rep := Analyze(recs, Thresholds{})
	var raise bool
	for _, h := range rep.LevelHints {
		if h.Command == "git" && h.Suggestion == "raise" {
			raise = true
		}
	}
	if !raise {
		t.Errorf("expected a 'raise' hint for never-reaccessed git, got %+v", rep.LevelHints)
	}
}

func TestAnalyze_RespectsMinRuns(t *testing.T) {
	// Only 2 runs — below MinRuns — so no next-formatter/hint suggestions.
	recs := []Record{
		compress("a", "make", 5000, 100, false, true),
		compress("b", "make", 5000, 100, false, true),
	}
	rep := Analyze(recs, Thresholds{})
	if len(rep.NextFormatters) != 0 {
		t.Errorf("below MinRuns should yield no next-formatter candidate, got %+v", rep.NextFormatters)
	}
	if len(rep.LevelHints) != 0 {
		t.Errorf("below MinRuns should yield no level hints, got %+v", rep.LevelHints)
	}
	// But the command still appears in the full Commands list.
	if len(rep.Commands) != 1 {
		t.Errorf("want make in Commands regardless of MinRuns, got %d", len(rep.Commands))
	}
}

func TestAnalyze_Deterministic(t *testing.T) {
	recs := []Record{
		compress("a", "make", 5000, 100, false, true),
		compress("b", "npm", 1500, 400, true, false),
		access("b", "npm"),
	}
	r1 := Analyze(recs, Thresholds{})
	r2 := Analyze(recs, Thresholds{})
	if r1.TotalRuns != r2.TotalRuns || len(r1.Commands) != len(r2.Commands) {
		t.Error("Analyze is not deterministic")
	}
}
