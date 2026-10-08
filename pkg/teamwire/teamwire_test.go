package teamwire

import (
	"encoding/json"
	"reflect"
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
}

// numericKinds are the field kinds that hold figures, not text.
var numericKinds = map[reflect.Kind]bool{
	reflect.Int: true, reflect.Int64: true, reflect.Float64: true,
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
			case numericKinds[ft.Kind()]:
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
		"schema":           func(u *Upload) { u.Schema = 2 },
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
