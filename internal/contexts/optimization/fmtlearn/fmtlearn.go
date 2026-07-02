// Package fmtlearn is the offline learning loop for command-output
// compression. It never changes runtime behaviour — the formatters stay
// deterministic. Instead it mines the telemetry every `tokenops fmt` run
// leaves behind (compression records + recovery re-access records) and
// produces a Report that proposes where the catalog should improve:
//
//   - NextFormatters: commands that keep falling back to the generic scrub
//     (no dedicated formatter, or the formatter declined) ranked by the raw
//     bytes at stake — the highest-ROI formatters to write next.
//   - CriticalMisses: commands whose compressed output was re-fetched from
//     the recovery store often, a signal the formatter dropped something the
//     agent needed and its rules should be tightened (or its default loss
//     level lowered).
//   - LevelHints: per-command loss-level tuning suggestions derived from the
//     re-access rate.
//
// Analyze is a pure function of the record slice; the report is advisory.
// Nothing here mutates a formatter — proposals become code changes gated by
// the golden corpus, preserving the critical-line survival guarantee.
package fmtlearn

import (
	"sort"
	"time"
)

// RecordType distinguishes the two telemetry rows the CLI appends.
type RecordType string

const (
	// RecordCompress is written once per `tokenops fmt` run.
	RecordCompress RecordType = "compress"
	// RecordAccess is written when the full output is re-fetched from the
	// recovery store (`tokenops fmt recover`), i.e. the compact view was
	// insufficient.
	RecordAccess RecordType = "access"
)

// Record is one telemetry row. Compress rows carry the compression metadata;
// access rows carry only Type, ID, Command, and TS (the ID links an access
// back to its compression).
type Record struct {
	Type            RecordType `json:"type"`
	ID              string     `json:"id"`
	Command         string     `json:"command"`
	Level           string     `json:"level,omitempty"`
	RawBytes        int64      `json:"raw_bytes,omitempty"`
	CompactBytes    int64      `json:"compact_bytes,omitempty"`
	TokensSaved     int64      `json:"tokens_saved,omitempty"`
	Handled         bool       `json:"handled,omitempty"`          // a command-specific formatter ran
	GenericFallback bool       `json:"generic_fallback,omitempty"` // fell back to the generic scrub
	CriticalKept    bool       `json:"critical_kept,omitempty"`
	TS              time.Time  `json:"ts"`
}

// CommandStat aggregates every record for one command token.
type CommandStat struct {
	Command      string  `json:"command"`
	Runs         int     `json:"runs"`
	GenericRuns  int     `json:"generic_runs"` // runs with no dedicated formatter or a fallback
	Accesses     int     `json:"accesses"`     // recovery re-fetches
	RawBytes     int64   `json:"raw_bytes"`
	TokensSaved  int64   `json:"tokens_saved"`
	GenericRatio float64 `json:"generic_ratio"` // GenericRuns / Runs
	AccessRate   float64 `json:"access_rate"`   // Accesses / Runs
}

// LevelHint is a per-command loss-level tuning suggestion.
type LevelHint struct {
	Command    string  `json:"command"`
	AccessRate float64 `json:"access_rate"`
	Suggestion string  `json:"suggestion"` // "lower" | "raise" | "keep"
	Rationale  string  `json:"rationale"`
}

// Report is the advisory output of Analyze.
type Report struct {
	TotalRuns      int           `json:"total_runs"`
	TotalAccesses  int           `json:"total_accesses"`
	TokensSaved    int64         `json:"tokens_saved"`
	Commands       []CommandStat `json:"commands"`        // all, by runs desc
	NextFormatters []CommandStat `json:"next_formatters"` // generic-heavy, by raw bytes desc
	CriticalMisses []CommandStat `json:"critical_misses"` // high access rate, by access rate desc
	LevelHints     []LevelHint   `json:"level_hints"`
}

// Thresholds tunes the report. Zero value yields the defaults below.
type Thresholds struct {
	// MinRuns is the minimum runs before a command is eligible for a
	// NextFormatter or LevelHint suggestion (avoids acting on one sample).
	MinRuns int
	// GenericRatioFloor: a command is a NextFormatter candidate when at
	// least this fraction of its runs hit the generic scrub.
	GenericRatioFloor float64
	// AccessRateCeiling: above this re-access rate a command is flagged as
	// a CriticalMiss (formatter too aggressive).
	AccessRateCeiling float64
	// AccessRateFloor: below this, an aggressive default is safe to raise.
	AccessRateFloor float64
}

func (t Thresholds) withDefaults() Thresholds {
	if t.MinRuns <= 0 {
		t.MinRuns = 5
	}
	if t.GenericRatioFloor <= 0 {
		t.GenericRatioFloor = 0.5
	}
	if t.AccessRateCeiling <= 0 {
		t.AccessRateCeiling = 0.10
	}
	if t.AccessRateFloor <= 0 {
		t.AccessRateFloor = 0.01
	}
	return t
}

// Analyze aggregates records into an advisory Report. It is pure: no I/O, no
// clock, deterministic ordering (ties broken by command name).
func Analyze(records []Record, th Thresholds) Report {
	th = th.withDefaults()

	stats := map[string]*CommandStat{}
	idCommand := map[string]string{} // compress id -> command, to attribute accesses
	rep := Report{}

	get := func(cmd string) *CommandStat {
		s := stats[cmd]
		if s == nil {
			s = &CommandStat{Command: cmd}
			stats[cmd] = s
		}
		return s
	}

	// First pass: compressions (establishes id->command).
	for _, r := range records {
		if r.Type != RecordCompress {
			continue
		}
		s := get(r.Command)
		s.Runs++
		s.RawBytes += r.RawBytes
		s.TokensSaved += r.TokensSaved
		if !r.Handled || r.GenericFallback {
			s.GenericRuns++
		}
		if r.ID != "" {
			idCommand[r.ID] = r.Command
		}
		rep.TotalRuns++
		rep.TokensSaved += r.TokensSaved
	}

	// Second pass: accesses, attributed via the compression id (or the
	// access row's own Command when the id is unknown).
	for _, r := range records {
		if r.Type != RecordAccess {
			continue
		}
		cmd := r.Command
		if c, ok := idCommand[r.ID]; ok {
			cmd = c
		}
		if cmd == "" {
			continue
		}
		get(cmd).Accesses++
		rep.TotalAccesses++
	}

	// Derive ratios and the ranked lists.
	for _, s := range stats {
		if s.Runs > 0 {
			s.GenericRatio = float64(s.GenericRuns) / float64(s.Runs)
			s.AccessRate = float64(s.Accesses) / float64(s.Runs)
		}
		rep.Commands = append(rep.Commands, *s)

		if s.Runs >= th.MinRuns && s.GenericRatio >= th.GenericRatioFloor {
			rep.NextFormatters = append(rep.NextFormatters, *s)
		}
		if s.Runs >= th.MinRuns && s.AccessRate > th.AccessRateCeiling {
			rep.CriticalMisses = append(rep.CriticalMisses, *s)
		}
		if s.Runs >= th.MinRuns {
			if h, ok := levelHint(*s, th); ok {
				rep.LevelHints = append(rep.LevelHints, h)
			}
		}
	}

	sort.Slice(rep.Commands, func(i, j int) bool {
		if rep.Commands[i].Runs != rep.Commands[j].Runs {
			return rep.Commands[i].Runs > rep.Commands[j].Runs
		}
		return rep.Commands[i].Command < rep.Commands[j].Command
	})
	sort.Slice(rep.NextFormatters, func(i, j int) bool {
		if rep.NextFormatters[i].RawBytes != rep.NextFormatters[j].RawBytes {
			return rep.NextFormatters[i].RawBytes > rep.NextFormatters[j].RawBytes
		}
		return rep.NextFormatters[i].Command < rep.NextFormatters[j].Command
	})
	sort.Slice(rep.CriticalMisses, func(i, j int) bool {
		if rep.CriticalMisses[i].AccessRate != rep.CriticalMisses[j].AccessRate {
			return rep.CriticalMisses[i].AccessRate > rep.CriticalMisses[j].AccessRate
		}
		return rep.CriticalMisses[i].Command < rep.CriticalMisses[j].Command
	})
	sort.Slice(rep.LevelHints, func(i, j int) bool {
		return rep.LevelHints[i].Command < rep.LevelHints[j].Command
	})
	return rep
}

// levelHint proposes a loss-level change from the re-access rate. Only
// commands with a dedicated formatter (some non-generic runs) get a hint —
// tuning a generic-only command's level is meaningless.
func levelHint(s CommandStat, th Thresholds) (LevelHint, bool) {
	if s.GenericRatio >= 1.0 {
		return LevelHint{}, false
	}
	switch {
	case s.AccessRate > th.AccessRateCeiling:
		return LevelHint{
			Command: s.Command, AccessRate: s.AccessRate, Suggestion: "lower",
			Rationale: "compressed output re-fetched often; formatter may be dropping needed lines",
		}, true
	case s.AccessRate <= th.AccessRateFloor:
		return LevelHint{
			Command: s.Command, AccessRate: s.AccessRate, Suggestion: "raise",
			Rationale: "compact output almost never re-fetched; a more aggressive default is safe",
		}, true
	default:
		return LevelHint{}, false
	}
}
