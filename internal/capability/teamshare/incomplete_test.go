package teamshare

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/capability/uniteconomics"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/pkg/eventschema"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

type unreadableEvents struct{ err error }

func (r unreadableEvents) ReadEvents(context.Context, eventschema.EventType, time.Time) ([]*eventschema.Envelope, error) {
	return nil, r.err
}

func (unreadableEvents) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return nil, nil
}

// The server replaces every day an upload covers, so an upload built
// while a reader failed would put days with figures missing over days
// that have them. It is not sent, and the attempt is recorded as such.
func TestSyncDoesNotSendAnUploadWithAPartUnread(t *testing.T) {
	unreadable := errors.New("database is locked")
	tests := []struct {
		name   string
		schema int
		role   string
		fail   func(*Deps)
		unread string
	}{
		{"transcripts read in part", teamwire.SchemaBuckets, "", func(d *Deps) {
			units, _ := d.Units(time.Time{}, now)
			d.Units = func(_, _ time.Time) ([]sessions.Unit, error) { return units[:1], unreadable }
		}, "transcripts"},
		{"event store", teamwire.SchemaBuckets, "", func(d *Deps) {
			d.Turns = func(context.Context, time.Time) ([]analytics.SessionTurn, error) { return nil, unreadable }
		}, "event store"},
		{"plan events", teamwire.SchemaFinOps, "", func(d *Deps) {
			cfg := &config.Config{Plans: map[string]string{"anthropic": "claude-max-20x"}}
			d.FinOps.Plans = func() headroom.Deps {
				return headroom.Deps{Config: cfg, Reader: unreadableEvents{unreadable}}
			}
		}, "plan days"},
		{"plan history", teamwire.SchemaFinOps, "", func(d *Deps) {
			read := d.FinOps.Plans
			d.FinOps.Plans = func() headroom.Deps {
				h := read()
				h.PlanHistory = func() (plans.History, error) { return plans.History{}, unreadable }
				return h
			}
		}, "plan days: read plan history"},
		{"waste", teamwire.SchemaFinOps, "", func(d *Deps) {
			d.FinOps.Waste = func(context.Context, TurnReader, time.Time, time.Time) ([]avoidable.Day, error) {
				return nil, unreadable
			}
		}, "waste"},
		{"units of work", teamwire.SchemaSpend, "", func(d *Deps) {
			d.Schema3.Placed = func(context.Context, TurnReader, time.Time, time.Time) ([]uniteconomics.Placed, error) {
				return nil, unreadable
			}
		}, "units"},
		{"a vendor's cost", teamwire.SchemaSpend, "owner", func(d *Deps) {
			readers := vendorReaders()
			readers["openai"] = func(context.Context, time.Time, time.Time) (map[string]float64, error) {
				return nil, unreadable
			}
			d.Schema3.VendorCosts = func(context.Context) map[string]VendorCostReader { return readers }
		}, "vendor cost (openai)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeServer{maxSchema: tt.schema, role: tt.role}
			path := filepath.Join(t.TempDir(), "team.json")
			if err := teamclient.Save(path, teamclient.State{URL: f.start(t), DeviceToken: "tok"}); err != nil {
				t.Fatal(err)
			}
			d := spendDeps()
			tt.fail(&d)

			_, err := Sync(context.Background(), d, Options{Days: 14, RepoNames: RepoRemote}, path, now)

			var incomplete *IncompleteError
			if !errors.As(err, &incomplete) {
				t.Fatalf("err = %v; want an upload that was not sent", err)
			}
			if len(f.bodies) != 0 {
				t.Errorf("the upload was sent with %s unread", tt.unread)
			}
			want := tt.unread + ": "
			named := false
			for _, u := range incomplete.Unread {
				named = named || (strings.HasPrefix(u, want) && strings.Contains(u, unreadable.Error()))
			}
			if !named {
				t.Errorf("unread = %q; want %q with its failure", incomplete.Unread, tt.unread)
			}
			st, err := teamclient.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if st.LastUpload == nil || !strings.HasPrefix(st.LastResult, "not sent: ") || !strings.Contains(st.LastResult, tt.unread) {
				t.Errorf("recorded result = %q; want it to say the upload was not sent and why", st.LastResult)
			}
		})
	}
}

// The same machine with every reader answering uploads as before.
func TestSyncSendsAnUploadReadInFull(t *testing.T) {
	f := &fakeServer{maxSchema: teamwire.SchemaSpend, role: "owner"}
	path := filepath.Join(t.TempDir(), "team.json")
	if err := teamclient.Save(path, teamclient.State{URL: f.start(t), DeviceToken: "tok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(context.Background(), spendDeps(), Options{Days: 14, RepoNames: RepoRemote}, path, now); err != nil {
		t.Fatal(err)
	}
	if len(f.bodies) != 1 {
		t.Fatalf("uploads sent = %d; want 1", len(f.bodies))
	}
	st, err := teamclient.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.LastResult, "sent ") {
		t.Errorf("recorded result = %q", st.LastResult)
	}
}
