# TokenOps ClickHouse Schema

Optional analytics tier for deployments that have outgrown the local
SQLite event store. The schema mirrors `pkg/eventschema` so the same
events flow without translation.

## Files

- `001_events.sql` — base tables: `tokenops.events`, `tokenops.optimizations`,
  `tokenops.coaching`, `tokenops.audit_log`. MergeTree, partitioned by
  month, ordered for the dashboard's primary scan keys.
- `002_materialized_views.sql` — pre-aggregated rollups the dashboard
  reads directly: hourly per-(provider, model) and per-workflow,
  daily per-(agent, model), and daily optimization savings.
- `003_kafka_pipeline.sql` — optional Kafka ingestion (set `kafka_broker_list`
  and `kafka_topic_list` to your environment). The TokenOps daemon
  dual-writes to this topic; the materialized view fans rows into
  `tokenops.events`.

## Apply

```sh
clickhouse-client --multiquery < 001_events.sql
clickhouse-client --multiquery < 002_materialized_views.sql
# Optional: only when running the Kafka pipeline.
clickhouse-client --multiquery < 003_kafka_pipeline.sql
```

## Direct insert

For low-volume self-hosted deployments, the daemon can write directly
to `tokenops.events` via the ClickHouse HTTP interface:

```sh
echo '{"id": "...", "schema_version": "1.0.0", "type": "prompt", ...}' | \
  curl --data-binary @- \
    'http://clickhouse:8123/?query=INSERT%20INTO%20tokenops.events%20FORMAT%20JSONEachRow'
```

The schema accepts the canonical JSON envelope with the `payload`
field flattened into native columns (provider, model, tokens, cost).
The full payload is also kept as JSON for forward-compat.

## Retention

ClickHouse TTL clauses are not set by default — operators choose their
retention windows per `pkg/eventschema` event type. Example: keep
prompts 90 days, optimization events 1 year, audit log 7 years:

```sql
ALTER TABLE tokenops.events
  MODIFY TTL day + INTERVAL 90 DAY;

ALTER TABLE tokenops.optimizations
  MODIFY TTL day + INTERVAL 1 YEAR;

ALTER TABLE tokenops.audit_log
  MODIFY TTL day + INTERVAL 7 YEAR;
```
