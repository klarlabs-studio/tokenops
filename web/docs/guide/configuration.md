# Configuration

The daemon reads `config.yaml` (path via `--config`) merged with
`TOKENOPS_*` environment variables; CLI flags win last.

## Reference

```yaml
listen: 127.0.0.1:7878        # bind address
log:
  level: info                 # debug | info | warn | error
  format: text                # text | json
shutdown:
  timeout: 15s                # graceful shutdown grace period

storage:
  enabled: true               # open the local sqlite store
  path: ~/.tokenops/events.db

tls:
  enabled: false              # serve HTTPS with auto-minted cert
  cert_dir: ~/.tokenops/certs
  hostnames: []               # extra SANs

providers:                    # upstream URL overrides
  openai: https://api.openai.com
  anthropic: https://api.anthropic.com
  gemini: https://generativelanguage.googleapis.com

otel:
  enabled: false              # ship envelopes to an OTLP collector
  endpoint: http://localhost:4318
  headers:
    x-honeycomb-team: ...
  service_name: tokenops
  redact: true
```

## Environment variables

| Variable                          | Maps to                       |
|-----------------------------------|-------------------------------|
| `TOKENOPS_LISTEN`                 | `listen`                      |
| `TOKENOPS_LOG_LEVEL`              | `log.level`                   |
| `TOKENOPS_LOG_FORMAT`             | `log.format`                  |
| `TOKENOPS_SHUTDOWN_TIMEOUT`       | `shutdown.timeout`            |
| `TOKENOPS_TLS_ENABLED`            | `tls.enabled`                 |
| `TOKENOPS_TLS_CERT_DIR`           | `tls.cert_dir`                |
| `TOKENOPS_STORAGE_ENABLED`        | `storage.enabled`             |
| `TOKENOPS_STORAGE_PATH`           | `storage.path`                |
| `TOKENOPS_OTEL_ENABLED`           | `otel.enabled`                |
| `TOKENOPS_OTEL_ENDPOINT`          | `otel.endpoint`               |
| `TOKENOPS_OTEL_SERVICE_NAME`      | `otel.service_name`           |
| `TOKENOPS_PROVIDER_OPENAI_URL`    | `providers.openai`            |
| `TOKENOPS_PROVIDER_ANTHROPIC_URL` | `providers.anthropic`         |
| `TOKENOPS_PROVIDER_GEMINI_URL`    | `providers.gemini`            |
