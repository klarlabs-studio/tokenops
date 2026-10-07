// Package opencodedb is the adapter for opencode's local SQLite store: it
// implements the telemetry/opencodedb Reader port every domain reads
// through, and answers the probes the infrastructure needs (the newest
// message, each session's directory).
//
// opencode 1.x keeps messages in `message` and their parts in `part`;
// opencode 2 keeps everything in `session_message`. During the transition
// both hold live data, so every message is read from whichever table has
// it, the 2.x row winning, and none is counted twice. What a message is
// stays in the domain package.
package opencodedb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite" // read-only driver for opencode.db

	"go.klarlabs.de/tokenops/internal/contexts/telemetry/opencodedb"
)

// Store reads opencode's database. The zero value is ready to use; it holds
// no connection, each call opens the file read-only and closes it.
type Store struct{}

var _ opencodedb.Reader = Store{}

// Read implements opencodedb.Reader.
func (Store) Read(path string, opts opencodedb.Options, visit func(opencodedb.Message) error) error {
	return Read(path, opts, visit)
}

// Read visits every message in the store at path. A missing store is
// not an error: opencode may not be installed.
func Read(path string, opts opencodedb.Options, visit func(opencodedb.Message) error) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	// mode=ro reads committed WAL data without blocking a running
	// opencode.
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return fmt.Errorf("opencodedb: open: %w", err)
	}
	defer func() { _ = db.Close() }()

	v2, v1 := hasTable(db, "session_message"), hasTable(db, "message")
	if !v1 && !v2 {
		return fmt.Errorf("%w: no message table in %s", opencodedb.ErrSchema, path)
	}
	seen := map[string]bool{}
	if v2 {
		if err := readV2(db, opts, seen, visit); err != nil {
			return err
		}
	}
	if v1 {
		if err := readV1(db, opts, seen, visit); err != nil {
			return err
		}
	}
	return nil
}

// Newest is the time of the newest message in either shape, and whether
// the store could be read.
func Newest(path string) (time.Time, bool) {
	if _, err := os.Stat(path); err != nil {
		return time.Time{}, false
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		return time.Time{}, false
	}
	defer func() { _ = db.Close() }()
	var newest int64
	read := false
	for _, table := range []string{"session_message", "message"} {
		if !hasTable(db, table) {
			continue
		}
		var ms sql.NullInt64
		if db.QueryRow(`SELECT MAX(time_created) FROM `+table).Scan(&ms) != nil {
			continue
		}
		read = true
		if ms.Valid && ms.Int64 > newest {
			newest = ms.Int64
		}
	}
	if newest == 0 {
		return time.Time{}, read
	}
	return time.UnixMilli(newest).UTC(), true
}

// sessionTable is the table holding sessions' directories for
// session_message rows: session_v2 in opencode 2, session in 1.18, which
// creates session_message first. Empty when neither has a directory.
func sessionTable(db *sql.DB) string {
	for _, t := range []string{"session_v2", "session"} {
		if hasTable(db, t) && hasColumn(db, t, "directory") {
			return t
		}
	}
	return ""
}

func hasColumn(db *sql.DB, table, column string) bool {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

func hasTable(db *sql.DB, name string) bool {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// v2Data is the JSON on a session_message row; which fields are set
// depends on its type.
type v2Data struct {
	Text  string `json:"text"`
	Model struct {
		ProviderID string `json:"providerID"`
		ID         string `json:"id"`
		Variant    string `json:"variant"`
	} `json:"model"`
	Content []struct {
		Type  string `json:"type"`
		Text  string `json:"text"`
		Name  string `json:"name"`
		State struct {
			Input json.RawMessage `json:"input"`
		} `json:"state"`
	} `json:"content"`
	Cost   float64 `json:"cost"`
	Tokens struct {
		Input     int64 `json:"input"`
		Output    int64 `json:"output"`
		Reasoning int64 `json:"reasoning"`
		Cache     struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
}

func readV2(db *sql.DB, opts opencodedb.Options, seen map[string]bool, visit func(opencodedb.Message) error) error {
	// A session's directory is in session_v2 once opencode has moved it;
	// 1.18 writes session_message while its sessions are still in session.
	from := `session_message m`
	dir := `''`
	if table := sessionTable(db); table != "" {
		from += ` LEFT JOIN ` + table + ` s ON s.id = m.session_id`
		dir = `coalesce(s.directory, '')`
	}
	query := `SELECT m.id, m.session_id, m.type, m.time_created, m.data, ` + dir + `
		FROM ` + from + `
		WHERE m.type IN ('user', 'assistant', 'compaction')`
	var args []any
	if opts.SessionID != "" {
		query += ` AND m.session_id = ?`
		args = append(args, opts.SessionID)
	}
	if !opts.Since.IsZero() {
		query += ` AND m.time_created >= ?`
		args = append(args, opts.Since.UnixMilli())
	}
	rows, err := db.Query(query+` ORDER BY m.time_created`, args...)
	if err != nil {
		return fmt.Errorf("%w: %v", opencodedb.ErrSchema, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, session, typ, data, dir string
		var created int64
		if rows.Scan(&id, &session, &typ, &created, &data, &dir) != nil {
			continue
		}
		seen[id] = true
		var d v2Data
		if json.Unmarshal([]byte(data), &d) != nil {
			continue
		}
		m := opencodedb.Message{
			ID: id, SessionID: session, Role: opencodedb.Role(typ), Created: time.UnixMilli(created).UTC(),
			ProviderID: d.Model.ProviderID, ModelID: d.Model.ID, Variant: d.Model.Variant,
			Cost: d.Cost, Root: dir, CWD: dir,
			Tokens: opencodedb.Tokens{
				Input: d.Tokens.Input, Output: d.Tokens.Output, Reasoning: d.Tokens.Reasoning,
				CacheRead: d.Tokens.Cache.Read, CacheWrite: d.Tokens.Cache.Write,
			},
		}
		if opts.Parts {
			if m.Role == opencodedb.User && strings.TrimSpace(d.Text) != "" {
				m.Text = []string{d.Text}
			}
			for _, c := range d.Content {
				switch c.Type {
				case "text":
					if strings.TrimSpace(c.Text) != "" {
						m.Text = append(m.Text, c.Text)
					}
				case "tool":
					m.Tools = append(m.Tools, opencodedb.Tool{Name: c.Name, Input: c.State.Input})
				}
			}
		}
		if err := visit(m); err != nil {
			return err
		}
	}
	return rows.Err()
}

// v1Message is the JSON on a 1.x message row.
type v1Message struct {
	Role       string  `json:"role"`
	ProviderID string  `json:"providerID"`
	ModelID    string  `json:"modelID"`
	Variant    string  `json:"variant"`
	Cost       float64 `json:"cost"`
	Model      struct {
		ProviderID string `json:"providerID"`
	} `json:"model"`
	Time struct {
		Created int64 `json:"created"`
	} `json:"time"`
	Path struct {
		CWD  string `json:"cwd"`
		Root string `json:"root"`
	} `json:"path"`
	Tokens struct {
		Input     int64 `json:"input"`
		Output    int64 `json:"output"`
		Reasoning int64 `json:"reasoning"`
		Cache     struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
}

// v1Parts is what a 1.x message's parts add to it.
type v1Parts struct {
	text       []string
	tools      []opencodedb.Tool
	compaction bool
}

func readV1(db *sql.DB, opts opencodedb.Options, seen map[string]bool, visit func(opencodedb.Message) error) error {
	var parts map[string]*v1Parts
	if opts.Parts && hasTable(db, "part") {
		parts = readV1Parts(db, opts.SessionID)
	}
	query := `SELECT id, session_id, data FROM message`
	var args []any
	if opts.SessionID != "" {
		query += ` WHERE session_id = ?`
		args = append(args, opts.SessionID)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return fmt.Errorf("%w: %v", opencodedb.ErrSchema, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, session, data string
		// A message 2.x has copied is read from there, with whatever
		// 2.x made of its parts: 2.x copies compactions as messages of
		// their own, so the 1.x compaction part is not read again.
		if rows.Scan(&id, &session, &data) != nil || seen[id] {
			continue
		}
		var d v1Message
		if json.Unmarshal([]byte(data), &d) != nil {
			continue
		}
		// A row without a creation time is still a turn that was paid
		// for; it has a zero Created, and a Since filter drops it.
		var created time.Time
		if d.Time.Created > 0 {
			created = time.UnixMilli(d.Time.Created).UTC()
		}
		if !opts.Since.IsZero() && created.Before(opts.Since) {
			continue
		}
		provider := d.ProviderID
		if provider == "" {
			provider = d.Model.ProviderID
		}
		m := opencodedb.Message{
			ID: id, SessionID: session, Role: opencodedb.Role(d.Role), Created: created,
			ProviderID: provider, ModelID: d.ModelID, Variant: d.Variant, Cost: d.Cost,
			Root: d.Path.Root, CWD: d.Path.CWD,
			Tokens: opencodedb.Tokens{
				Input: d.Tokens.Input, Output: d.Tokens.Output, Reasoning: d.Tokens.Reasoning,
				CacheRead: d.Tokens.Cache.Read, CacheWrite: d.Tokens.Cache.Write,
			},
		}
		p := parts[id]
		if p != nil {
			m.Text, m.Tools = p.text, p.tools
		}
		if m.Role == opencodedb.User || m.Role == opencodedb.Assistant {
			if err := visit(m); err != nil {
				return err
			}
		}
		// 1.x marks a compaction with a part on the message that
		// triggered it; 2.x gives it a message of its own.
		if p != nil && p.compaction {
			if err := visit(opencodedb.Message{ID: id + "#compaction", SessionID: session, Role: opencodedb.Compaction, Created: created,
				ProviderID: provider, Root: m.Root, CWD: m.CWD}); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

// readV1Parts gathers each 1.x message's text, tool calls and
// compaction marker. A failure yields what was read: a missing part
// degrades one signal, where failing would lose every metric.
func readV1Parts(db *sql.DB, session string) map[string]*v1Parts {
	out := map[string]*v1Parts{}
	query := `SELECT message_id, data FROM part`
	var args []any
	if session != "" {
		query = `SELECT p.message_id, p.data FROM part p JOIN message m ON m.id = p.message_id WHERE m.session_id = ?`
		args = append(args, session)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var messageID, data string
		if rows.Scan(&messageID, &data) != nil {
			continue
		}
		var p struct {
			Type      string `json:"type"`
			Text      string `json:"text"`
			Synthetic bool   `json:"synthetic"`
			Tool      string `json:"tool"`
			State     struct {
				Input json.RawMessage `json:"input"`
			} `json:"state"`
		}
		if json.Unmarshal([]byte(data), &p) != nil {
			continue
		}
		e := out[messageID]
		if e == nil {
			e = &v1Parts{}
			out[messageID] = e
		}
		switch p.Type {
		case "text":
			if !p.Synthetic && strings.TrimSpace(p.Text) != "" {
				e.text = append(e.text, p.Text)
			}
		case "tool":
			e.tools = append(e.tools, opencodedb.Tool{Name: p.Tool, Input: p.State.Input})
		case "compaction":
			e.compaction = true
		}
	}
	return out
}

// SessionDirs maps each opencode session to the directory it ran in,
// from whichever session table holds it. A non-zero since keeps the
// sessions active since then, by the table's update (or else creation)
// time; a table that records neither is read whole.
func SessionDirs(path string, since time.Time) (map[string]string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("opencodedb: open: %w", err)
	}
	defer func() { _ = db.Close() }()
	out := map[string]string{}
	for _, table := range []string{"session", "session_v2"} {
		if !hasTable(db, table) || !hasColumn(db, table, "directory") {
			continue
		}
		query, args := `SELECT id, directory FROM `+table, []any{}
		if col := activityColumn(db, table); col != "" && !since.IsZero() {
			query += ` WHERE ` + col + ` >= ?`
			args = append(args, since.UnixMilli())
		}
		rows, err := db.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", opencodedb.ErrSchema, err)
		}
		for rows.Next() {
			var id, dir string
			if rows.Scan(&id, &dir) == nil && dir != "" {
				out[id] = dir // session_v2, read second, wins
			}
		}
		_ = rows.Close()
	}
	return out, nil
}

// activityColumn names the column that says when a session was last
// active, or "" when the table records no time.
func activityColumn(db *sql.DB, table string) string {
	for _, col := range []string{"time_updated", "time_created"} {
		if hasColumn(db, table, col) {
			return col
		}
	}
	return ""
}
