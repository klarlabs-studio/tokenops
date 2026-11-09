package plans

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// newestTwo keeps each source's newest usable reading and the one before
// it, ties in their recorded order, and parses only the readings it needs
// to find them, not two weeks of them.
func TestNewestTwoParsesOnlyTheNewest(t *testing.T) {
	base := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	events := make([]*eventschema.Envelope, 0, 1005)
	for i := range 1000 {
		events = append(events, &eventschema.Envelope{Source: "a", Timestamp: base.Add(time.Duration(i) * time.Minute),
			Attributes: map[string]string{"n": string(rune('0' + i%10))}})
	}
	tieFirst := &eventschema.Envelope{Source: "b", Timestamp: base, Attributes: map[string]string{"n": "first"}}
	tieSecond := &eventschema.Envelope{Source: "b", Timestamp: base, Attributes: map[string]string{"n": "second"}}
	unusable := &eventschema.Envelope{Source: "a", Timestamp: base.Add(48 * time.Hour), Attributes: map[string]string{"n": "x"}}
	other := &eventschema.Envelope{Source: "c", Timestamp: base.Add(72 * time.Hour), Attributes: map[string]string{}}
	events = append(events, tieFirst, tieSecond, unusable, other, nil)

	asked := 0
	newest, prior := newestTwo(events, []string{"a", "b"}, func(e *eventschema.Envelope) bool {
		asked++
		return e.Attributes["n"] != "x"
	})
	if newest["a"] != events[999] || prior["a"] != events[998] {
		t.Errorf("a: newest %v, prior %v; want the last two minutes", newest["a"], prior["a"])
	}
	if newest["b"] != tieFirst || prior["b"] != tieSecond {
		t.Errorf("b: a tie is not kept in recorded order")
	}
	if _, ok := newest["c"]; ok {
		t.Error("a source not asked for was read")
	}
	if asked > 5 {
		t.Errorf("usable asked of %d readings; want the newest few only", asked)
	}
}
