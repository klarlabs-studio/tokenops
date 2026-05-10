package sqlite

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
}

type migration struct {
	Version int
	Name    string
	SQL     string
}
