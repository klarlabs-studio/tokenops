// Package sqlitetest builds large event stores for tests and benchmarks.
//
// Start-up regressions only show on a store the size a daemon reaches
// after months of use (300k+ envelopes); appending that many through the
// store takes half a minute, so Fill copies one real envelope row in SQL.
package sqlitetest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Fill adds n prompt envelopes to store, shaped like the pollers': ids
// are a source prefix and a 64-bit hex digest (spread over the id index
// as real digests are), timestamps one every 20 seconds over the last n
// intervals, and each payload a real PromptEvent with about a kilobyte of
// attributes. Every row decodes as an envelope.
func Fill(tb testing.TB, store *sqlite.Store, n int) {
	tb.Helper()
	ctx := context.Background()
	const tmpl = "sqlitetest-template"
	start := time.Now().Add(-time.Duration(n) * 20 * time.Second)
	if err := store.Append(ctx, &eventschema.Envelope{
		ID: tmpl, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: start, Source: "claude-code-jsonl",
		Attributes: map[string]string{"pad": strings.Repeat("x", 900)},
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-4",
			InputTokens: 1200, OutputTokens: 300, TotalTokens: 1500,
		},
	}); err != nil {
		tb.Fatalf("sqlitetest: template: %v", err)
	}
	cols, err := columns(ctx, store)
	if err != nil {
		tb.Fatalf("sqlitetest: columns: %v", err)
	}
	var sel []string
	for _, c := range cols {
		switch c {
		case "id":
			// Multiplying by a large constant scatters the ids over the
			// index the way digests are, rather than appending; it stays
			// within int64 (and so distinct) for any n a test uses.
			sel = append(sel, "printf('ccj-%016x', n * 2654435761)")
		case "timestamp_ns":
			sel = append(sel, fmt.Sprintf("timestamp_ns + n * %d", int64(20*time.Second)))
		case "day":
			sel = append(sel, fmt.Sprintf("(timestamp_ns + n * %d) / %d", int64(20*time.Second), int64(24*time.Hour)))
		default:
			sel = append(sel, c)
		}
	}
	q := `WITH RECURSIVE cnt(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM cnt WHERE n < ?)
	      INSERT INTO events (` + strings.Join(cols, ", ") + `)
	      SELECT ` + strings.Join(sel, ", ") + ` FROM events, cnt WHERE events.id = ?`
	if _, err := store.DB().ExecContext(ctx, q, n, tmpl); err != nil {
		tb.Fatalf("sqlitetest: fill: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `DELETE FROM events WHERE id = ?`, tmpl); err != nil {
		tb.Fatalf("sqlitetest: drop template: %v", err)
	}
}

// columns lists the events table's columns in declaration order.
func columns(ctx context.Context, store *sqlite.Store) ([]string, error) {
	rows, err := store.DB().QueryContext(ctx, `SELECT name FROM pragma_table_info('events')`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
