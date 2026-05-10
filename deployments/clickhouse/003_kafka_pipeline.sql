-- Optional Kafka pipeline for high-volume deployments. The TokenOps
-- daemon can dual-write events to a Kafka topic; this table consumes
-- the topic via the Kafka engine and a materialized view fans rows
-- into the events table. Direct INSERT into tokenops.events still
-- works alongside this pipeline.
--
-- Brokers and topic name are placeholders — operators set them per
-- environment.

CREATE TABLE IF NOT EXISTS tokenops.events_kafka
(
    raw String
)
ENGINE = Kafka
SETTINGS
    kafka_broker_list = 'kafka:9092',
    kafka_topic_list  = 'tokenops.events',
    kafka_group_name  = 'tokenops-clickhouse',
    kafka_format      = 'JSONAsString',
    kafka_num_consumers = 2;

-- The materialized view parses the JSON envelope and writes into the
-- main events table. JSONExtract* functions degrade gracefully when a
-- field is absent (return zero values), so partial envelopes survive
-- without dropping the row.
CREATE MATERIALIZED VIEW IF NOT EXISTS tokenops.events_kafka_to_events
TO tokenops.events AS
SELECT
    JSONExtractString(raw, 'id')              AS id,
    JSONExtractString(raw, 'schema_version')  AS schema_version,
    JSONExtractString(raw, 'type')            AS type,
    parseDateTime64BestEffortOrZero(JSONExtractString(raw, 'timestamp')) AS timestamp,
    JSONExtractString(raw, 'trace_id')        AS trace_id,
    JSONExtractString(raw, 'span_id')         AS span_id,
    JSONExtractString(raw, 'source')          AS source,
    JSONExtractString(JSONExtractRaw(raw, 'payload'), 'provider')      AS provider,
    JSONExtractString(JSONExtractRaw(raw, 'payload'), 'request_model') AS model,
    JSONExtractString(JSONExtractRaw(raw, 'payload'), 'workflow_id')   AS workflow_id,
    JSONExtractString(JSONExtractRaw(raw, 'payload'), 'agent_id')      AS agent_id,
    JSONExtractString(JSONExtractRaw(raw, 'payload'), 'session_id')    AS session_id,
    JSONExtractString(JSONExtractRaw(raw, 'payload'), 'user_id')       AS user_id,
    JSONExtractUInt(JSONExtractRaw(raw, 'payload'),  'input_tokens')   AS input_tokens,
    JSONExtractUInt(JSONExtractRaw(raw, 'payload'),  'output_tokens')  AS output_tokens,
    JSONExtractUInt(JSONExtractRaw(raw, 'payload'),  'total_tokens')   AS total_tokens,
    JSONExtractUInt(JSONExtractRaw(raw, 'payload'),  'cached_input_tokens') AS cached_input_tokens,
    JSONExtractFloat(JSONExtractRaw(raw, 'payload'), 'cost_usd')       AS cost_usd,
    JSONExtractInt(JSONExtractRaw(raw, 'payload'),   'latency_ns')     AS latency_ns,
    JSONExtractInt(JSONExtractRaw(raw, 'payload'),   'time_to_first_token_ns') AS ttft_ns,
    toUInt8(JSONExtractBool(JSONExtractRaw(raw, 'payload'), 'streaming')) AS streaming,
    toUInt16(JSONExtractInt(JSONExtractRaw(raw, 'payload'), 'status')) AS status,
    JSONExtractString(JSONExtractRaw(raw, 'payload'), 'finish_reason') AS finish_reason,
    JSONExtractRaw(raw, 'payload')                                     AS payload,
    JSONExtractRaw(raw, 'attributes')                                  AS attributes
FROM tokenops.events_kafka;
