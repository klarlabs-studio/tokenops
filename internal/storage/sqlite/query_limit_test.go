package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A capped Query keeps the newest rows in the window, returned oldest
// first: `--limit 3` on a busy week shows this week's last three events,
// not its first three.
func TestQueryLimitKeepsNewestRowsAscending(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	envs := make([]*eventschema.Envelope, 0, 10)
	for i := range 10 {
		envs = append(envs, mustPromptEnvelope(t, fmt.Sprintf("e-%02d", i), base.Add(time.Duration(i)*time.Minute),
			&eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-4o"}))
	}
	if err := s.AppendBatch(ctx, envs); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		filter Filter
		want   []string
	}{
		{"limit keeps newest", Filter{Limit: 3}, []string{"e-07", "e-08", "e-09"}},
		{"limit within window", Filter{Limit: 2, Since: base.Add(2 * time.Minute), Until: base.Add(6 * time.Minute)},
			[]string{"e-04", "e-05"}},
		{"limit above count returns all ascending", Filter{Limit: 100, Since: base.Add(8 * time.Minute)},
			[]string{"e-08", "e-09"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.Query(ctx, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, len(got))
			for i, e := range got {
				ids[i] = e.ID
			}
			if fmt.Sprint(ids) != fmt.Sprint(tc.want) {
				t.Fatalf("ids = %v, want %v", ids, tc.want)
			}
		})
	}
}

// Rows sharing a timestamp are cut and ordered by id, so a capped read is
// deterministic.
func TestQueryLimitBreaksTimestampTiesByID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	envs := make([]*eventschema.Envelope, 0, 3)
	for _, id := range []string{"c", "a", "b"} {
		envs = append(envs, mustPromptEnvelope(t, id, at, &eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI}))
	}
	if err := s.AppendBatch(ctx, envs); err != nil {
		t.Fatal(err)
	}
	got, err := s.Query(ctx, Filter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "c" {
		t.Fatalf("got %v, want [b c]", []string{got[0].ID, got[1].ID})
	}
}
