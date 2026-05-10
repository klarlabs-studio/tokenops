-- TokenOps event store — ClickHouse schema.
-- Mirrors pkg/eventschema's Envelope shape: a flat row per event with
-- the full typed payload kept as JSON so new field additions land
-- without a migration. The same indexed projection columns the SQLite
-- store materialises (provider, model, workflow_id, agent_id, ...) live
-- here as native columns so analytical queries hit the column store.

CREATE DATABASE IF NOT EXISTS tokenops;

-- Raw event firehose. Insert path is direct INSERT or via a Kafka
-- engine table feeding it (see 002_kafka_pipeline.sql). The MergeTree
-- ORDER BY is tuned for the dashboard's most common scans: time slice +
-- workflow drill-down.
CREATE TABLE IF NOT EXISTS tokenops.events
(
    id              String,
    schema_version  LowCardinality(String),
    type            LowCardinality(String),
    timestamp       DateTime64(9, 'UTC'),
    day             Date MATERIALIZED toDate(timestamp),
    trace_id        String,
    span_id         String,
    source          LowCardinality(String),

    provider        LowCardinality(String),
    model           LowCardinality(String),
    workflow_id     String,
    agent_id        String,
    session_id      String,
    user_id         String,

    input_tokens    UInt64,
    output_tokens   UInt64,
    total_tokens   UInt64,
    cached_input_tokens UInt64,
    cost_usd        Float64,

    latency_ns      Int64,
    ttft_ns         Int64,
    streaming       UInt8,
    status          UInt16,
    finish_reason   LowCardinality(String),

    payload         String CODEC(ZSTD(3)),
    attributes      String CODEC(ZSTD(3))
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(day)
ORDER BY (day, type, workflow_id, timestamp)
SETTINGS index_granularity = 8192;

-- Optimization events get their own table because they are produced at
-- a different cadence and rarely joined with PromptEvents in the same
-- query (the rollup is computed offline).
CREATE TABLE IF NOT EXISTS tokenops.optimizations
(
    id              String,
    timestamp       DateTime64(9, 'UTC'),
    day             Date MATERIALIZED toDate(timestamp),
    prompt_hash     String,
    kind            LowCardinality(String),
    mode            LowCardinality(String),
    decision        LowCardinality(String),
    reason          String,
    estimated_savings_tokens Int64,
    estimated_savings_usd    Float64,
    quality_score   Float32,
    latency_impact_ns Int64,
    workflow_id     String,
    agent_id        String,
    payload         String CODEC(ZSTD(3))
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(day)
ORDER BY (day, kind, decision, timestamp);

-- Coaching events follow the same pattern.
CREATE TABLE IF NOT EXISTS tokenops.coaching
(
    id              String,
    timestamp       DateTime64(9, 'UTC'),
    day             Date MATERIALIZED toDate(timestamp),
    workflow_id     String,
    agent_id        String,
    session_id      String,
    kind            LowCardinality(String),
    summary         String,
    estimated_savings_tokens Int64,
    estimated_savings_usd    Float64,
    efficiency_score Float32,
    payload         String CODEC(ZSTD(3))
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(day)
ORDER BY (day, kind, timestamp);

-- Audit log mirror — append-only, retained per compliance window.
CREATE TABLE IF NOT EXISTS tokenops.audit_log
(
    id          String,
    timestamp   DateTime64(9, 'UTC'),
    day         Date MATERIALIZED toDate(timestamp),
    action      LowCardinality(String),
    actor       String,
    target      String,
    details     String CODEC(ZSTD(3))
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(day)
ORDER BY (day, action, timestamp);
