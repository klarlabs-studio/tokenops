package teamshare

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/uniteconomics"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// spendDeps is the machine of deps, with commits and a pull request
// placed in its repository and an admin key for Anthropic's costs.
func spendDeps() Deps {
	d := finopsDeps()
	turns, _ := d.Turns(context.Background(), time.Time{})
	turns[0].Provider, turns[1].Provider, turns[2].Provider = "anthropic", "anthropic", "openai"
	d.Turns = func(context.Context, time.Time) ([]analytics.SessionTurn, error) { return turns, nil }
	t0 := now.Add(-2 * time.Hour)
	d.Schema3 = Schema3{
		Placed: func(context.Context, TurnReader, time.Time, time.Time) ([]uniteconomics.Placed, error) {
			p := []uniteconomics.Placed{
				{Unit: uniteconomics.UnitCommit, Root: "/Users/alice/secret-project", At: t0.Add(5 * time.Minute)},
				{Unit: uniteconomics.UnitMergedPR, Root: "/Users/alice/secret-project", At: t0.Add(time.Hour)},
				// Landed before the window: left out.
				{Unit: uniteconomics.UnitCommit, Root: "/Users/alice/secret-project", At: now.AddDate(0, 0, -30)},
			}
			p[0].APIEquivalentUSD, p[1].APIEquivalentUSD = 0.5, 0.5
			return p, nil
		},
		VendorCosts: func(context.Context) map[string]VendorCostReader { return vendorReaders() },
	}
	return d
}

func vendorReaders() map[string]VendorCostReader {
	return map[string]VendorCostReader{
		"anthropic": func(_ context.Context, start, end time.Time) (map[string]float64, error) {
			out := map[string]float64{}
			for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
				out[d.Format(teamwire.DayLayout)] = 10
			}
			return out, nil
		},
	}
}

// A schema 3 upload is built from the same machine full of prompts,
// paths, model names and session IDs, and carries none of them either.
func TestSchema3UploadCarriesNoContent(t *testing.T) {
	u, warnings := Build(context.Background(), spendDeps(),
		Options{Days: 14, RepoNames: RepoRemote, Schema: teamwire.SchemaSpend, Role: "owner"}, now)
	if len(warnings) > 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if u.Schema != teamwire.SchemaSpend || len(u.Spend) == 0 || len(u.Units) == 0 || len(u.VendorCosts) == 0 {
		t.Fatalf("schema 3 upload = %+v", u)
	}
	if err := u.Validate(); err != nil {
		t.Fatalf("schema 3 upload does not validate: %v", err)
	}
	raw, _ := json.Marshal(u)
	for _, c := range canaries {
		if strings.Contains(string(raw), c) {
			t.Errorf("upload carries %q:\n%s", c, raw)
		}
	}
}

// The spend rows are the buckets' turns by provider: the same tokens and
// value per day and repository.
func TestSpendRowsAreTheBucketsByProvider(t *testing.T) {
	u, _ := Build(context.Background(), spendDeps(), Options{Days: 14, RepoNames: RepoRemote, Schema: teamwire.SchemaSpend}, now)
	type key struct{ day, repo string }
	buckets, spend := map[key][2]float64{}, map[key][2]float64{}
	for _, b := range u.Buckets {
		v := buckets[key{b.Day, b.Repo}]
		buckets[key{b.Day, b.Repo}] = [2]float64{v[0] + float64(b.Tokens), v[1] + b.APIEquivalentUSD}
	}
	for _, s := range u.Spend {
		v := spend[key{s.Day, s.Repo}]
		spend[key{s.Day, s.Repo}] = [2]float64{v[0] + float64(s.Tokens), v[1] + s.APIEquivalentUSD}
	}
	if len(buckets) != len(spend) {
		t.Fatalf("buckets %v, spend %v", buckets, spend)
	}
	for k, v := range buckets {
		if spend[k] != v {
			t.Errorf("%v: buckets %v, spend %v", k, v, spend[k])
		}
	}
	if len(u.Spend) != 2 || u.Spend[0].Provider != "anthropic" || u.Spend[0].UnpricedTurns != 1 ||
		u.Spend[1].Repo != teamwire.RepoNone || u.Spend[1].Provider != "openai" {
		t.Errorf("spend = %+v", u.Spend)
	}
}

// Units: a task with recorded usage, a commit and a pull request in the
// repository of their session, nothing from before the window.
func TestUnitRows(t *testing.T) {
	u, _ := Build(context.Background(), spendDeps(), Options{Days: 14, RepoNames: RepoRemote, Schema: teamwire.SchemaSpend}, now)
	var api *teamwire.UnitBucket
	for i := range u.Units {
		if u.Units[i].Repo == "acme/api" {
			api = &u.Units[i]
		}
	}
	if api == nil || api.Tasks != 1 || api.TaskUSD != 0.5 || api.Commits != 1 || api.CommitUSD != 0.5 ||
		api.MergedPRs != 1 || api.MergedPRUSD != 0.5 {
		t.Errorf("units = %+v", u.Units)
	}
}

// Vendor cost is read only for an owner or admin, leaves out the day the
// vendor has not reported, and a reader that fails is a warning.
func TestVendorCostOnlyForOwnersAndAdmins(t *testing.T) {
	opts := Options{Days: 3, RepoNames: RepoRemote, Schema: teamwire.SchemaSpend}
	for role, want := range map[string]int{"": 0, "member": 0, "lead": 0, "admin": 2, "owner": 2} {
		opts.Role = role
		asked := false
		d := spendDeps()
		d.Schema3.VendorCosts = func(context.Context) map[string]VendorCostReader { asked = true; return vendorReaders() }
		u, _ := Build(context.Background(), d, opts, now)
		if asked != (want > 0) {
			t.Errorf("role %q: admin keys looked up: %v", role, asked)
		}
		if len(u.VendorCosts) != want {
			t.Errorf("role %q: %d vendor costs, want %d", role, len(u.VendorCosts), want)
		}
		for _, v := range u.VendorCosts {
			if v.Day == now.Format(teamwire.DayLayout) {
				t.Errorf("today sent: %+v", v)
			}
		}
	}
	d := spendDeps()
	readers := vendorReaders()
	readers["openai"] = func(context.Context, time.Time, time.Time) (map[string]float64, error) {
		return nil, errors.New("401")
	}
	d.Schema3.VendorCosts = func(context.Context) map[string]VendorCostReader { return readers }
	opts.Role = "owner"
	u, warnings := Build(context.Background(), d, opts, now)
	if len(u.VendorCosts) != 2 || len(warnings) != 1 || !strings.Contains(warnings[0], "openai") {
		t.Errorf("vendor costs %+v, warnings %v", u.VendorCosts, warnings)
	}
}

// The role comes from the server with max_schema; a server that dropped
// the vendor cost is asked again before the next upload.
func TestSyncSendsVendorCostByTheServersRole(t *testing.T) {
	f := &fakeServer{maxSchema: 3, role: "owner", ignoreVendorCost: true}
	path := filepath.Join(t.TempDir(), "team.json")
	if err := teamclient.Save(path, teamclient.State{URL: f.start(t), DeviceToken: "tok"}); err != nil {
		t.Fatal(err)
	}
	res, err := Sync(context.Background(), spendDeps(), Options{Days: 3}, path, now)
	if err != nil || len(res.Upload.VendorCosts) == 0 {
		t.Fatalf("vendor costs %+v, err %v", res.Upload.VendorCosts, err)
	}
	st, _ := teamclient.Load(path)
	if st.Role != "owner" || st.SchemaCheckedAt != nil || !strings.Contains(st.LastResult, "vendor cost dropped") {
		t.Errorf("state = %+v", st)
	}
}
