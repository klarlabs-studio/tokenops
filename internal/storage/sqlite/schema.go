package sqlite

import (
	"fmt"
	"time"
)

// migrations are applied sequentially; once committed, a row is recorded in
// schema_migrations and the migration is skipped on subsequent boots. New
// migrations append to this slice — never reorder or rewrite existing entries.
var migrations = []migration{
	{
		Version: 1,
		Name:    "events_table",
		SQL: `
CREATE TABLE events (
    id              TEXT PRIMARY KEY,
    schema_version  TEXT NOT NULL,
    type            TEXT NOT NULL,
    timestamp_ns    INTEGER NOT NULL,
    day             INTEGER NOT NULL,
    trace_id        TEXT,
    span_id         TEXT,
    source          TEXT,
    provider        TEXT,
    model           TEXT,
    workflow_id     TEXT,
    agent_id        TEXT,
    session_id      TEXT,
    user_id         TEXT,
    input_tokens    INTEGER,
    output_tokens   INTEGER,
    total_tokens    INTEGER,
    cost_usd        REAL,
    payload         TEXT NOT NULL,
    attributes      TEXT
) STRICT;

CREATE INDEX events_timestamp_idx     ON events (timestamp_ns);
CREATE INDEX events_day_type_idx      ON events (day, type);
CREATE INDEX events_type_ts_idx       ON events (type, timestamp_ns);
CREATE INDEX events_workflow_idx      ON events (workflow_id, timestamp_ns) WHERE workflow_id IS NOT NULL;
CREATE INDEX events_agent_idx         ON events (agent_id, timestamp_ns)    WHERE agent_id    IS NOT NULL;
CREATE INDEX events_session_idx       ON events (session_id, timestamp_ns)  WHERE session_id  IS NOT NULL;
CREATE INDEX events_provider_model_idx ON events (provider, model, timestamp_ns) WHERE provider IS NOT NULL;
`,
	},
	{
		Version: 2,
		Name:    "audit_log_table",
		SQL: `
CREATE TABLE audit_log (
    id            TEXT PRIMARY KEY,
    timestamp_ns  INTEGER NOT NULL,
    action        TEXT NOT NULL,
    actor         TEXT NOT NULL,
    target        TEXT,
    details       TEXT
) STRICT;

CREATE INDEX audit_log_timestamp_idx ON audit_log (timestamp_ns);
CREATE INDEX audit_log_action_idx    ON audit_log (action, timestamp_ns);
CREATE INDEX audit_log_actor_idx     ON audit_log (actor, timestamp_ns);
`,
	},
	{
		Version: 3,
		Name:    "event_work_association",
		SQL: `
-- Ties an event to the work ontology introduced in ADR 0004 Phase 3.
--
-- The existing workflow_id / agent_id / session_id columns are
-- attribution invented before that ontology, and none of them says which
-- attempt at which goal produced the event. They stay: every existing
-- query uses them. These are added beside them.
--
-- Columns rather than a JSON field in the payload because rolling an
-- attempt's consumption up to the goal it served has to be a query, not
-- a scan of every row.
ALTER TABLE events ADD COLUMN work_id      TEXT;
ALTER TABLE events ADD COLUMN execution_id TEXT;
ALTER TABLE events ADD COLUMN actor_id     TEXT;

CREATE INDEX events_work_idx      ON events (work_id, timestamp_ns)      WHERE work_id      IS NOT NULL;
CREATE INDEX events_execution_idx ON events (execution_id, timestamp_ns) WHERE execution_id IS NOT NULL;
CREATE INDEX events_actor_idx     ON events (actor_id, timestamp_ns)     WHERE actor_id     IS NOT NULL;
`,
	},
	{
		Version: 4,
		Name:    "event_control_correlation",
		SQL: `
-- Decision, intervention and experiment ids are separate from trace ids:
-- tracing describes execution mechanics, while these ids join the evidence
-- needed to answer why TokenOps acted and what happened afterwards.
ALTER TABLE events ADD COLUMN decision_id     TEXT;
ALTER TABLE events ADD COLUMN intervention_id TEXT;
ALTER TABLE events ADD COLUMN experiment_id   TEXT;

CREATE INDEX events_decision_idx     ON events (decision_id, timestamp_ns)     WHERE decision_id     IS NOT NULL;
CREATE INDEX events_intervention_idx ON events (intervention_id, timestamp_ns) WHERE intervention_id IS NOT NULL;
CREATE INDEX events_experiment_idx   ON events (experiment_id, timestamp_ns)   WHERE experiment_id   IS NOT NULL;
`,
	},
	{
		Version: 5,
		Name:    "events_usage_covering_index",
		SQL: `
-- Every spend rollup (summary, series, burn rate, forecast, top) reads a
-- window of prompt events and sums their usage. Through (type,
-- timestamp_ns) alone each matching row was fetched from the table, whose
-- rows carry the payload and attributes: a month of one provider's events
-- read ~90 MB of pages and parsed four JSON fields per row, five times per
-- request. GET /api/spend/summary?since=720h took over five seconds.
--
-- This index holds every column and payload expression those queries read,
-- so they never touch the table. The expressions must stay identical to
-- the ones in analytics.go (costSourceMetered, costSourcePlanCovered,
-- cacheReadExpr, cacheWriteExpr, cacheWrite1hExpr): SQLite only answers
-- from an index expression it can match, and TestUsageQueriesAreCovered
-- fails if one drifts. Building it reads the table once.
CREATE INDEX events_usage_idx ON events (
    type, timestamp_ns, provider, model, source,
    input_tokens, output_tokens, total_tokens, cost_usd,
    COALESCE(json_extract(payload, '$.cost_source'), ''),
    CAST(COALESCE(json_extract(payload, '$.cached_input_tokens'), json_extract(attributes, '$.cache_read_input')) AS INTEGER),
    CAST(COALESCE(json_extract(payload, '$.cache_write_input_tokens'), json_extract(attributes, '$.cache_creation_input')) AS INTEGER),
    CAST(json_extract(payload, '$.cache_write_1h_input_tokens') AS INTEGER)
);
`,
	},
	{
		Version: 6,
		Name:    "events_generation",
		SQL: `
-- Counts every change to a stored event other than an insert: an update
-- (re-attribution, a plan restamp) or a delete (retention). New events
-- are found by rowid; this is how a reader keeping events in memory
-- (EventCache) learns that ones it already holds changed, whichever
-- process or release changed them. A trigger rather than application code
-- because every process on the machine writes this store.
CREATE TABLE events_generation (
    id  INTEGER PRIMARY KEY CHECK (id = 1),
    gen INTEGER NOT NULL
) STRICT;
INSERT INTO events_generation (id, gen) VALUES (1, 0);

CREATE TRIGGER events_generation_on_update AFTER UPDATE ON events
BEGIN
    UPDATE events_generation SET gen = gen + 1 WHERE id = 1;
END;

CREATE TRIGGER events_generation_on_delete AFTER DELETE ON events
BEGIN
    UPDATE events_generation SET gen = gen + 1 WHERE id = 1;
END;
`,
	},
	{
		Version: 7,
		Name:    "events_changes",
		SQL:     eventsChangesMigration,
	},
}

// eventsChangesMigration replaces events_generation (migration 6) with a
// log of which events changed, so a reader keeping events in memory
// (EventCache) re-reads those rows instead of everything.
//
// A count said only that something changed: every plan restamp, start-up
// re-attribution and retention pass made the next glance reload a month of
// events, about a second on a busy store. The log names each changed row
// by rowid; new rows are still found by rowid alone, so inserts — nearly
// every write — log nothing.
//
// Only changes to events younger than changeLogHorizon are logged: no
// cached read reaches that far back (maxCacheSpan), so an older row is one
// no cache answers with. Retention deletes old rows, so its passes log
// nothing, and a large delete costs a comparison per row rather than a
// second write. The log keeps its latest changeLogKeep entries; a reader
// further behind reloads. Triggers rather than application code because
// every process on the machine writes this store, older releases too.
var eventsChangesMigration = fmt.Sprintf(`
DROP TRIGGER events_generation_on_update;
DROP TRIGGER events_generation_on_delete;
DROP TABLE events_generation;

-- seq orders the changes; AUTOINCREMENT keeps it from ever going back,
-- so a reader's position in the log stays meaningful. row is the rowid of
-- the event that changed.
CREATE TABLE events_changes (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    row INTEGER NOT NULL
) STRICT;

CREATE TRIGGER events_changes_on_update AFTER UPDATE ON events
WHEN max(old.timestamp_ns, new.timestamp_ns) >= %[1]s
BEGIN
    INSERT INTO events_changes (row) VALUES (new.rowid);
    INSERT INTO events_changes (row) SELECT old.rowid WHERE old.rowid <> new.rowid;
    DELETE FROM events_changes WHERE seq <= last_insert_rowid() - %[2]d;
END;

CREATE TRIGGER events_changes_on_delete AFTER DELETE ON events
WHEN old.timestamp_ns >= %[1]s
BEGIN
    INSERT INTO events_changes (row) VALUES (old.rowid);
    DELETE FROM events_changes WHERE seq <= last_insert_rowid() - %[2]d;
END;
`, changeLogHorizonSQL, changeLogKeep)

// changeLogHorizon is how far back a change to an event is logged; see
// eventsChangesMigration. The cache's span stays well inside it.
const changeLogHorizon = 45 * 24 * time.Hour

// changeLogHorizonSQL is changeLogHorizon before now, in timestamp_ns.
var changeLogHorizonSQL = fmt.Sprintf("(CAST(strftime('%%s', 'now') AS INTEGER) - %d) * 1000000000",
	int64(changeLogHorizon/time.Second))

// changeLogKeep is how many changes the log keeps.
const changeLogKeep = 65536

type migration struct {
	Version int
	Name    string
	SQL     string
}
