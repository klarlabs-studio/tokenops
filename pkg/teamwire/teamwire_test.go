package teamwire

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// constrainedStrings is every string an Upload may carry, each with the
// rule that keeps free text out of it. A string field that is not listed
// here fails TestUploadHasNoFreeText: adding one is a change to the privacy
// boundary and needs ADR 0012 amended, a validator, and an entry here.
var constrainedStrings = map[string]string{
	"Upload.BatchID":       "ValidUUID",
	"Upload.ClientVersion": "ValidLabel",
	"Upload.Days":          "DayLayout date",
	"Bucket.Day":           "DayLayout date",
	"Bucket.Repo":          "ValidRepo: name or owner/name, no whitespace, no path",
	"Bucket.Kind":          "ValidKind: enumeration",
	// Schema 2 (#685).
	"PlanDay.Day":                "DayLayout date",
	"PlanDay.Provider":           "ValidProvider: the provider registry",
	"PlanDay.Plan":               "ValidPlan: the plan catalog",
	"Waste.Day":                  "DayLayout date",
	"Waste.Repo":                 "ValidRepo",
	"Waste.Kind":                 "ValidKind: enumeration",
	"Waste.Class":                "ValidWasteClass: enumeration",
	"SavingsDay.Day":             "DayLayout date",
	"PlanAdvice.Provider":        "ValidProvider: the provider registry",
	"PlanAdvice.Plan":            "ValidPlan: the plan catalog",
	"PlanAdvice.Verdict":         "AdviceVerdicts: enumeration",
	"PlanAdvice.Kind":            "AdviceKinds: enumeration",
	"PlanAdvice.RecommendedPlan": "ValidPlan: the plan catalog, or empty for the API",
	"PlanAdvice.Confidence":      "AdviceConfidences: enumeration",
	// Schema 3 (ADR 0012 §11).
	"SpendBucket.Day":      "DayLayout date",
	"SpendBucket.Repo":     "ValidRepo",
	"SpendBucket.Provider": "ValidProvider: the provider registry",
	"UnitBucket.Day":       "DayLayout date",
	"UnitBucket.Repo":      "ValidRepo",
	"VendorCost.Day":       "DayLayout date",
	"VendorCost.Provider":  "ValidProvider: the provider registry",
	// Schema 5 (ADR 0012, Upload schema 5).
	"Upload.PreventedWeeks":   "DayLayout date, a Monday: the UTC week",
	"PreventedWeek.Week":      "DayLayout date, a Monday: the UTC week",
	"PreventedWeek.Mechanism": "ValidMechanism: enumeration",
	// Schema 6 (ADR 0012, Upload schema 6).
	"AdviceOption.Kind":    "AdviceKinds: enumeration",
	"AdviceOption.Plan":    "ValidPlan: the plan catalog of the advice's provider, or empty for the API",
	"PlanBinding.Provider": "ValidProvider: the provider registry",
	"PlanBinding.Plan":     "ValidPlan: the plan catalog",
	"PlanBinding.Since":    "DayLayout date, not after computed_at, or empty",
	"PlanWindow.Provider":  "ValidProvider: the provider registry",
	"PlanWindow.Window":    "weekly or monthly: enumeration, never a 5-hour window",
	"PlanWindow.ResetsOn":  "DayLayout date within the window's length of computed_at: no instant",
	"PlanWindow.Basis":     "average or recent: enumeration",
}

// boundedFigures is every figure a schema 5 prevented row and the schema 6
// additions carry, each
// with the bound the server enforces. A figure added to PreventedWeek is a
// change to what crosses the boundary: it needs a bound here and a check in
// Validate, the same as a string does above.
var boundedFigures = map[string]string{
	"PreventedWeek.Count":          "at least 1 (a row counts an action)",
	"PreventedWeek.Tokens":         "0 or more, when known",
	"PreventedWeek.BlockedMinutes": "0 to MaxBlockedMinutes (7x1440), lockout rows only; absent when 0",
	"PreventedWeek.USD":            "0 or more, absent when not priced",
	// Schema 6 (ADR 0012, Upload schema 6).
	"PlanDay.PlanValueUSD":           "0 to 1e7, absent when not priced",
	"PlanDay.PlanUnpricedTurns":      "0 or more, only beside plan_value_usd",
	"PlanAdvice.ListValueUSD":        "0 to 1e7, absent when not priced",
	"AdviceOption.MonthlyUSD":        "0 to 1e7, absent when the fee is unknown",
	"AdviceOption.Lockouts":          "0 or more",
	"AdviceOption.BlockedMinutes":    "0 to MaxAdviceDays x 1440",
	"AdviceOption.PeakPct":           "0 to MaxPeakPct (10000), absent when not measured",
	"PlanBinding.Shared":             "a boolean the member sets",
	"PlanWindow.UsedPct":             "0 to 100, whole points",
	"PlanWindow.ProjectedPctAtReset": "0 to 999, whole points; 0 when too early",
	"PlanWindow.RunsOut":             "a boolean, true only at 100 or more",
	"PlanWindow.ShortHours":          "0 to 168 in steps of 6, only when it runs out",
	"PlanWindow.TooEarly":            "a boolean; no projection with it",
}

// boundedPrefixes are the types and fields from schema 5 on whose figures
// must each state a bound in boundedFigures.
var boundedPrefixes = []string{"PreventedWeek.", "AdviceOption.", "PlanBinding.", "PlanWindow.",
	"PlanDay.PlanValueUSD", "PlanDay.PlanUnpricedTurns", "PlanAdvice.ListValueUSD"}

// numericKinds are the field kinds that hold figures, not text.
var numericKinds = map[reflect.Kind]bool{
	reflect.Int: true, reflect.Int64: true, reflect.Float64: true, reflect.Bool: true,
}

// TestUploadHasNoFreeText walks the upload's type and fails on any field
// that could carry text the rules above do not constrain: a new string, a
// map, an interface, a byte slice. It is the structural half of "only
// derived metrics cross the boundary"; Validate is the runtime half.
func TestUploadHasNoFreeText(t *testing.T) {
	seen := map[string]bool{}
	var walk func(typ reflect.Type)
	walk = func(typ reflect.Type) {
		for i := range typ.NumField() {
			f := typ.Field(i)
			name := typ.Name() + "." + f.Name
			seen[name] = true
			ft := f.Type
			switch {
			case ft == reflect.TypeOf(time.Time{}):
			case numericKinds[ft.Kind()], ft.Kind() == reflect.Pointer && numericKinds[ft.Elem().Kind()]:
				// A figure, or an optional one absent when it could not be
				// measured. Schema 5's and 6's figures state their bounds.
				if slices.ContainsFunc(boundedPrefixes, func(p string) bool { return strings.HasPrefix(name, p) }) {
					if _, ok := boundedFigures[name]; !ok {
						t.Errorf("%s is a figure with no stated bound; list it in boundedFigures "+
							"and check it in Validate (ADR 0012, Upload schemas 5 and 6)", name)
					}
				}
			case ft.Kind() == reflect.String, ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.String:
				if _, ok := constrainedStrings[name]; !ok {
					t.Errorf("%s is a string with no rule against free text; prompts, paths, "+
						"transcripts and commit messages must not be able to cross the boundary "+
						"(ADR 0012). Constrain it and list it in constrainedStrings", name)
				}
			case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
				walk(ft.Elem())
			default:
				t.Errorf("%s has kind %s, which can carry arbitrary content; the upload holds "+
					"figures, enumerations and constrained labels only", name, ft.Kind())
			}
		}
	}
	walk(reflect.TypeOf(Upload{}))
	for name := range constrainedStrings {
		if !seen[name] {
			t.Errorf("constrainedStrings lists %s, which the upload no longer has; delete it", name)
		}
	}
	for name := range boundedFigures {
		if !seen[name] {
			t.Errorf("boundedFigures lists %s, which the upload no longer has; delete it", name)
		}
	}
}

func validUpload() Upload {
	return Upload{
		Schema: SchemaVersion, BatchID: "0b7e4a52-7f6c-4c3e-9d1e-2a1f0c9b8a77",
		ComputedAt: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), ClientVersion: "0.102.0",
		Days: []string{"2026-10-07", "2026-10-08"},
		Buckets: []Bucket{
			{Day: "2026-10-08", Repo: "klarlabs/tokenops", Kind: KindEdit, Instructions: 3, FirstTry: 2, Tokens: 1200, CostUSD: 0.4},
			{Day: "2026-10-07", Repo: RepoNone, Kind: KindUnknown, Turns: 1},
		},
	}
}

func TestValidate(t *testing.T) {
	if err := validUpload().Validate(); err != nil {
		t.Fatalf("valid upload rejected: %v", err)
	}
	cases := map[string]func(u *Upload){
		"schema":           func(u *Upload) { u.Schema = SchemaVersion + 1 },
		"batch id":         func(u *Upload) { u.BatchID = "batch 1" },
		"no days":          func(u *Upload) { u.Days = nil },
		"bad day":          func(u *Upload) { u.Days[0] = "yesterday" },
		"day not covered":  func(u *Upload) { u.Buckets[0].Day = "2026-10-01" },
		"absolute path":    func(u *Upload) { u.Buckets[0].Repo = "/Users/alice/work/secret-project" },
		"relative path":    func(u *Upload) { u.Buckets[0].Repo = "work/secret/project" },
		"home path":        func(u *Upload) { u.Buckets[0].Repo = "~/src" },
		"sentence":         func(u *Upload) { u.Buckets[0].Repo = "fix the login bug" },
		"long label":       func(u *Upload) { u.Buckets[0].Repo = strings.Repeat("a", 65) },
		"kind":             func(u *Upload) { u.Buckets[0].Kind = "refactor the parser" },
		"negative":         func(u *Upload) { u.Buckets[0].Tokens = -1 },
		"first try > all":  func(u *Upload) { u.Buckets[0].FirstTry = 4 },
		"duplicate bucket": func(u *Upload) { u.Buckets[1] = u.Buckets[0] },
		"client version":   func(u *Upload) { u.ClientVersion = "a b" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			u := validUpload()
			u.Days = append([]string(nil), u.Days...)
			u.Buckets = append([]Bucket(nil), u.Buckets...)
			mutate(&u)
			if err := u.Validate(); err == nil {
				t.Errorf("accepted %+v", u)
			}
		})
	}
}

// The JSON field names are the wire contract; a rename breaks every
// deployed server.
func TestBucketJSONNames(t *testing.T) {
	raw, err := json.Marshal(validUpload().Buckets[0])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"day", "repo", "kind", "sessions", "instructions", "turns", "tool_calls",
		"active_seconds", "first_try", "reworked", "interrupted", "escalated", "rejected", "tokens",
		"cost_usd", "api_equivalent_usd", "unpriced_turns"} {
		if _, ok := m[k]; !ok {
			t.Errorf("bucket JSON lacks %q", k)
		}
		delete(m, k)
	}
	if len(m) > 0 {
		t.Errorf("unexpected bucket fields: %v", m)
	}
}
