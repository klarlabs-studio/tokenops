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
}

type migration struct {
	Version int
	Name    string
	SQL     string
}
