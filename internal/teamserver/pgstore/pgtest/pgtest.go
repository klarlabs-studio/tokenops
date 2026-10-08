// Package pgtest gives a test its own migrated team-plane store in a
// throwaway schema of the Postgres named by TOKENOPS_TEST_POSTGRES, and
// skips the test when that variable is unset. CI sets it (the test job runs
// a Postgres service); locally, point it at any disposable database:
//
//	docker run -d --rm -p 127.0.0.1:55432:5432 -e POSTGRES_PASSWORD=test postgres:17-alpine
//	export TOKENOPS_TEST_POSTGRES=postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"go.klarlabs.de/tokenops/internal/teamserver/pgstore"
)

// EnvDSN names the variable holding the test database's URL.
const EnvDSN = "TOKENOPS_TEST_POSTGRES"

// New returns a migrated store in a fresh schema, dropped when the test
// ends.
func New(t *testing.T) *pgstore.Store {
	t.Helper()
	dsn := os.Getenv(EnvDSN)
	if dsn == "" {
		t.Skipf("%s is not set; skipping the Postgres integration test", EnvDSN)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var raw [6]byte
	_, _ = rand.Read(raw[:])
	schema := "teamtest_" + hex.EncodeToString(raw[:])

	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect %s: %v", EnvDSN, err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create schema: %v", err)
	}
	_ = admin.Close(ctx)

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", EnvDSN, err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	store, err := pgstore.Open(ctx, u.String())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := store.Migrate(ctx); err != nil {
		store.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if c, err := pgx.Connect(ctx, dsn); err == nil {
			_, _ = c.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
			_ = c.Close(ctx)
		}
	})
	return store
}
