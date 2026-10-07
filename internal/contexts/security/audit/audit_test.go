package audit

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"
)

// memStore is an in-memory Store. The SQL implementation, with its
// filters and ordering, is tested in internal/storage/sqlite.
type memStore struct {
	mu      sync.Mutex
	entries []Entry
	err     error
}

func (m *memStore) AppendAudit(_ context.Context, e Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.entries = append(m.entries, e)
	return nil
}

func (m *memStore) QueryAudit(_ context.Context, f Filter) ([]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]Entry(nil), m.entries...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func TestRecordMintsIDAndTimestamp(t *testing.T) {
	store := &memStore{}
	got, err := NewRecorder(store).Record(context.Background(), Entry{Action: ActionConfigChange, Actor: "a"})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if got.ID == "" || got.Timestamp.IsZero() {
		t.Errorf("entry not completed: %+v", got)
	}
	if len(store.entries) != 1 || store.entries[0].ID != got.ID {
		t.Errorf("store holds %+v, want the returned entry", store.entries)
	}
}

func TestRecordKeepsGivenIDAndTimestamp(t *testing.T) {
	at := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	got, err := NewRecorder(&memStore{}).Record(context.Background(), Entry{ID: "x", Timestamp: at, Action: ActionConfigChange, Actor: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "x" || !got.Timestamp.Equal(at) {
		t.Errorf("entry rewritten: %+v", got)
	}
}

func TestRecordRequiresActionAndActor(t *testing.T) {
	store := &memStore{}
	rec := NewRecorder(store)
	ctx := context.Background()
	if _, err := rec.Record(ctx, Entry{Actor: "a"}); err == nil {
		t.Error("expected action error")
	}
	if _, err := rec.Record(ctx, Entry{Action: ActionConfigChange}); err == nil {
		t.Error("expected actor error")
	}
	if len(store.entries) != 0 {
		t.Errorf("invalid entries persisted: %+v", store.entries)
	}
}

func TestRecordSurfacesStoreError(t *testing.T) {
	boom := errors.New("boom")
	if _, err := NewRecorder(&memStore{err: boom}).Record(context.Background(), Entry{Action: ActionConfigChange, Actor: "a"}); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the store's", err)
	}
}

func TestQueryDefaultsLimit(t *testing.T) {
	var got Filter
	spy := spyStore{query: func(f Filter) { got = f }}
	if _, err := NewRecorder(spy).Query(context.Background(), Filter{}); err != nil {
		t.Fatal(err)
	}
	if got.Limit != defaultQueryLimit {
		t.Errorf("limit = %d, want %d", got.Limit, defaultQueryLimit)
	}
	if _, err := NewRecorder(spy).Query(context.Background(), Filter{Limit: 2}); err != nil {
		t.Fatal(err)
	}
	if got.Limit != 2 {
		t.Errorf("limit = %d, want the caller's 2", got.Limit)
	}
}

type spyStore struct{ query func(Filter) }

func (spyStore) AppendAudit(context.Context, Entry) error { return nil }
func (s spyStore) QueryAudit(_ context.Context, f Filter) ([]Entry, error) {
	s.query(f)
	return nil, nil
}

func TestNilRecorder(t *testing.T) {
	var r *Recorder
	if _, err := r.Record(context.Background(), Entry{Action: ActionConfigChange, Actor: "a"}); !errors.Is(err, ErrNotInitialised) {
		t.Errorf("err = %v, want ErrNotInitialised", err)
	}
	if _, err := NewRecorder(nil).Query(context.Background(), Filter{}); !errors.Is(err, ErrNotInitialised) {
		t.Errorf("err = %v, want ErrNotInitialised", err)
	}
}
