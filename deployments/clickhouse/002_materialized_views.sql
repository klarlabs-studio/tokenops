-- Materialized aggregates for the dashboard's hot paths. Each view
-- maintains a per-bucket pre-aggregation that the API layer can scan
-- in O(buckets) instead of O(events). Bucket widths mirror the SQLite
-- analytics package: hourly + daily.

-- Hourly per-(provider, model) rollup.
CREATE MATERIALIZED VIEW IF NOT EXISTS tokenops.events_hourly_provider_model
ENGINE = SummingMergeTree
PARTITION BY toYYYYMM(bucket)
ORDER BY (bucket, provider, model)
POPULATE AS
SELECT
    toStartOfHour(timestamp)         AS bucket,
    provider,
    model,
    count()                          AS requests,
    sum(input_tokens)                AS input_tokens,
    sum(output_tokens)               AS output_tokens,
    sum(total_tokens)                AS total_tokens,
    sum(cost_usd)                    AS cost_usd
FROM tokenops.events
WHERE type = 'prompt'
GROUP BY bucket, provider, model;

-- Hourly per-workflow rollup.
CREATE MATERIALIZED VIEW IF NOT EXISTS tokenops.events_hourly_workflow
ENGINE = SummingMergeTree
PARTITION BY toYYYYMM(bucket)
ORDER BY (bucket, workflow_id)
POPULATE AS
SELECT
    toStartOfHour(timestamp)         AS bucket,
    workflow_id,
    count()                          AS requests,
    sum(input_tokens)                AS input_tokens,
    sum(output_tokens)               AS output_tokens,
    sum(total_tokens)                AS total_tokens,
    sum(cost_usd)                    AS cost_usd
FROM tokenops.events
WHERE type = 'prompt' AND workflow_id != ''
GROUP BY bucket, workflow_id;

-- Daily per-(agent, model) rollup for forecasting / weekly burn views.
CREATE MATERIALIZED VIEW IF NOT EXISTS tokenops.events_daily_agent_model
ENGINE = SummingMergeTree
PARTITION BY toYYYYMM(bucket)
ORDER BY (bucket, agent_id, model)
POPULATE AS
SELECT
    toDate(timestamp)                AS bucket,
    agent_id,
    model,
    count()                          AS requests,
    sum(input_tokens)                AS input_tokens,
    sum(output_tokens)               AS output_tokens,
    sum(total_tokens)                AS total_tokens,
    sum(cost_usd)                    AS cost_usd
FROM tokenops.events
WHERE type = 'prompt' AND agent_id != ''
GROUP BY bucket, agent_id, model;

-- Daily optimization-savings rollup.
CREATE MATERIALIZED VIEW IF NOT EXISTS tokenops.optimizations_daily
ENGINE = SummingMergeTree
PARTITION BY toYYYYMM(bucket)
ORDER BY (bucket, kind, decision)
POPULATE AS
SELECT
    toDate(timestamp)                AS bucket,
    kind,
    decision,
    count()                          AS events,
    sum(estimated_savings_tokens)    AS estimated_savings_tokens,
    sum(estimated_savings_usd)       AS estimated_savings_usd
FROM tokenops.optimizations
GROUP BY bucket, kind, decision;
