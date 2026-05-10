# Gemini CLI

Google's Gemini CLI uses the `google-genai` SDK. Set its base URL
override before launch and every call routes through TokenOps.

## Setup

1. Start the daemon:

   ```bash
   TOKENOPS_STORAGE_ENABLED=true tokenops start
   ```

2. Point the Gemini CLI at the proxy. The CLI honors
   `GOOGLE_GENAI_HTTP_OPTIONS_BASE_URL` (Python SDK env knob); set it
   before launch:

   ```bash
   export GEMINI_API_KEY="<gemini-api-key>"
   export GOOGLE_GENAI_HTTP_OPTIONS_BASE_URL="http://127.0.0.1:7878/gemini"
   ```

   For older builds without the env knob, edit the CLI's config to set
   `http_options.base_url`.

3. Run as usual:

   ```bash
   gemini
   ```

## Smoke test

```bash
./scripts/smoketest-gemini.sh
```

The script issues a single `generateContent` request through the proxy
and verifies a PromptEvent persisted in the local store.

## Troubleshooting

- **404 on the proxy** — the request used a path the proxy does not
  recognise (e.g. an experimental v1alpha endpoint). Check
  `tokenops status` and the daemon log for "unknown path".
- **TLS / SAN mismatch** — Gemini SDK does not honour the system trust
  store on every platform. Run TokenOps with TLS off in dev (default)
  or trust the local CA via `tokenops cert install`.
