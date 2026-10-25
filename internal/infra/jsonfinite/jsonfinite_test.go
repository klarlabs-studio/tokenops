package jsonfinite

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

type band struct {
	Label string  `json:"label"`
	Rate  float64 `json:"rate_pct"`
}

type metrics struct {
	Median float64            `json:"median"`
	Plain  float32            // no tag: encoding/json names it Plain
	Skip   float64            `json:"-"`
	ByKey  map[string]metrics `json:"by_key,omitempty"`
	Ptr    *band              `json:"ptr,omitempty"`
	Any    any                `json:"any,omitempty"`
}

type snapshot struct {
	Metrics metrics `json:"metrics"`
	Bands   []band  `json:"bands"`
	Fixed   [2]float64
	hidden  float64
}

// One figure that is not a number must not cost the whole document: each
// such value is zeroed where it sits and named by its JSON path, and
// everything else is left alone.
func TestSanitizeZeroesNonFiniteFloatsAndNamesThem(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	s := snapshot{
		Metrics: metrics{
			Median: 4.5,
			Plain:  float32(math.Inf(-1)),
			Skip:   nan,
			ByKey:  map[string]metrics{"b": {Median: nan}, "a": {Median: 1}},
			Ptr:    &band{Label: "p", Rate: inf},
			Any:    &band{Rate: nan},
		},
		Bands:  []band{{Label: "<200k", Rate: 3.1}, {Label: ">800k", Rate: nan}},
		Fixed:  [2]float64{1, nan},
		hidden: nan,
	}
	got := Sanitize(&s)
	want := []string{
		"metrics.Plain",
		"metrics.by_key.b.median",
		"metrics.ptr.rate_pct",
		"metrics.any.rate_pct",
		"bands[1].rate_pct",
		"Fixed[1]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("paths = %q\nwant    %q", got, want)
	}
	if s.Metrics.Median != 4.5 || s.Bands[0].Rate != 3.1 || s.Metrics.ByKey["a"].Median != 1 || s.Bands[1].Label != ">800k" {
		t.Errorf("finite values changed: %+v", s)
	}
	if _, err := json.Marshal(s); err != nil {
		t.Errorf("still does not encode: %v", err)
	}
}

func TestSanitizeLeavesFiniteValuesAndNilAlone(t *testing.T) {
	if got := Sanitize(nil); got != nil {
		t.Errorf("nil = %q", got)
	}
	s := snapshot{Bands: []band{{Rate: 1}}}
	if got := Sanitize(&s); got != nil {
		t.Errorf("finite = %q", got)
	}
}
