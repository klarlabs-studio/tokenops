// Package pgstore is the team plane's Postgres store (ADR 0012): schema
// migrations, credentials kept as hashes, idempotent ingestion of derived
// figures, aggregate reads and the audit log.
package pgstore

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrationLock is the advisory-lock key that serialises migrations when
// several server processes start at once.
const migrationLock = 0x746f6b656e6f7073 // "tokenops"

type migration struct {
	version int
	name    string
	sql     string
}

func migrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		name := e.Name()
		num, _, ok := strings.Cut(name, "_")
		if !ok || !strings.HasSuffix(name, ".sql") {
			return nil, fmt.Errorf("migration %s: want NNNN_name.sql", name)
		}
		v, err := strconv.Atoi(num)
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", name, err)
		}
		body, err := fs.ReadFile(migrationFS, "migrations/"+name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: name, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration %s: versions must run 1, 2, 3… without gaps", m.name)
		}
	}
	return out, nil
}

// Migrate applies every migration not yet applied, each in its own
// transaction, under an advisory lock. It returns the versions it applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool) ([]int, error) {
	ms, err := migrations()
	if err != nil {
		return nil, err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(migrationLock)); err != nil {
		return nil, fmt.Errorf("migration lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, int64(migrationLock)) }()
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version int PRIMARY KEY, name text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, fmt.Errorf("schema_migrations: %w", err)
	}
	applied := map[int]bool{}
	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var done []int
	for _, m := range ms {
		if applied[m.version] {
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return done, err
		}
		if _, err := tx.Exec(ctx, m.sql); err != nil {
			_ = tx.Rollback(ctx)
			return done, fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, m.version, m.name); err != nil {
			_ = tx.Rollback(ctx)
			return done, err
		}
		if err := tx.Commit(ctx); err != nil {
			return done, err
		}
		done = append(done, m.version)
	}
	return done, nil
}
