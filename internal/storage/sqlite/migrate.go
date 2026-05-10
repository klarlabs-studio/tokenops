package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// migrate brings the database schema up to the latest version by applying
// any migrations from the migrations slice that have not yet been recorded
// in schema_migrations. Each migration runs in its own transaction so a
// partial schema does not leak.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schemaMigrationsDDL); err != nil {
		return fmt.Errorf("sqlite: create schema_migrations: %w", err)
	}

	applied, err := loadAppliedVersions(ctx, s.db)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		if err := applyMigration(ctx, s.db, m); err != nil {
			return err
		}
	}
	return nil
}

const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    applied_at INTEGER NOT NULL
) STRICT;
`

func loadAppliedVersions(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	applied := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("sqlite: scan migration version: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate migrations: %w", err)
	}
	return applied, nil
}

func applyMigration(ctx context.Context, db *sql.DB, m migration) (err error) {
	if m.Version <= 0 {
		return errors.New("sqlite: migration version must be positive")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin migration %d: %w", m.Version, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("sqlite: apply migration %d (%s): %w", m.Version, m.Name, err)
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.Version, m.Name, nowUnixNano(),
	); err != nil {
		return fmt.Errorf("sqlite: record migration %d: %w", m.Version, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit migration %d: %w", m.Version, err)
	}
	return nil
}
