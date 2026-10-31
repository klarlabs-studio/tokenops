package teamshare

import (
	"context"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/infra/gitlog"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// An upload's parts resolve directories through one git cache: with a
// cache each, the first upload after a start resolved every session
// directory three times.
func TestMachineDepsShareOneGitCache(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "e.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	eng := spend.NewEngine(spend.DefaultTable())

	d, waste, placed := machineDeps(config.Config{}, nil, store, eng, nil)
	repos, ok := d.Repos.(*gitlog.Repos)
	if !ok || repos == nil {
		t.Fatalf("the upload's repositories have no git cache: %T", d.Repos)
	}
	if waste.Repos != repos {
		t.Error("the waste report keeps a git cache of its own")
	}
	if placed.Repos != repos {
		t.Error("the units of work keep a git cache of their own")
	}
	if d.FinOps.Waste == nil || d.Schema3.Placed == nil {
		t.Fatal("the waste report or the units of work are not read")
	}

	// Without an event store there is no waste report; the units of work
	// still share the cache.
	d, _, placed = machineDeps(config.Config{}, nil, nil, nil, nil)
	if d.FinOps.Waste != nil {
		t.Error("a waste report without an event store")
	}
	if placed.Repos == nil || placed.Repos != d.Repos {
		t.Error("the units of work keep a git cache of their own")
	}
}
