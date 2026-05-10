# Gemini CLI

Google's Gemini CLI honors `GOOGLE_GENAI_HTTP_OPTIONS_BASE_URL`.

## Setup

```bash
TOKENOPS_STORAGE_ENABLED=true tokenops start
export GEMINI_API_KEY="<gemini-api-key>"
export GOOGLE_GENAI_HTTP_OPTIONS_BASE_URL="http://127.0.0.1:7878/gemini"
gemini
```

## Smoke test

```bash
./scripts/smoketest-gemini.sh
```
