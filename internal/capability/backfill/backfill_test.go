package backfill

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type memStore struct{ envs []*eventschema.Envelope }

func (m *memStore) Append(_ context.Context, env *eventschema.Envelope) error {
	m.envs = append(m.envs, env)
	return nil
}

// A backfill reads every page the Admin API hands back, not the first.
func TestAnthropicBackfillReadsEveryPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		row := `{"uncached_input_tokens":100,"output_tokens":10,"model":"claude-sonnet-5","cache_creation":{}}`
		if r.URL.Query().Get("page") == "" {
			_, _ = w.Write([]byte(`{"data":[{"starting_at":"2026-10-01T00:00:00Z","ending_at":"2026-10-01T01:00:00Z","results":[` + row + `]}],"has_more":true,"next_page":"p2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"starting_at":"2026-10-02T00:00:00Z","ending_at":"2026-10-02T01:00:00Z","results":[` + row + `,{"model":"zero","cache_creation":{}}]}],"has_more":false}`))
	}))
	defer srv.Close()

	store := &memStore{}
	got, err := Anthropic(context.Background(), store, AnthropicRequest{AdminKey: "sk-ant-admin-test", Hours: 48, BaseURL: srv.URL},
		time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got.Buckets != 2 || got.Inserted != 2 || got.Skipped != 1 || len(store.envs) != 2 {
		t.Errorf("result %+v, stored %d; want both pages' rows stored and the zero row skipped", got, len(store.envs))
	}

	dry := &memStore{}
	if got, _ := Anthropic(context.Background(), dry, AnthropicRequest{AdminKey: "k", Hours: 48, BaseURL: srv.URL, DryRun: true}, time.Now()); got.Inserted != 2 || len(dry.envs) != 0 {
		t.Errorf("dry run %+v stored %d", got, len(dry.envs))
	}
}
