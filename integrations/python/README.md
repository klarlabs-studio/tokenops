# Python integrations

Two thin Python packages pair with the TokenOps proxy:

- [`tokenops_langchain`](./tokenops_langchain) — LangChain
  `BaseCallbackHandler` that stamps attribution headers on every LLM
  call and configures the SDK base URLs.
- [`tokenops_openai_agents`](./tokenops_openai_agents) —
  `AsyncOpenAI` subclass for the OpenAI Agents SDK with attribution
  headers pinned and the proxy base URL pre-applied.

Both packages route requests through the local TokenOps daemon and
attach the standard `X-Tokenops-Workflow-Id` / `X-Tokenops-Agent-Id`
headers so the proxy can stitch related calls into workflow traces.

## Examples

- [`examples/langchain_workflow.py`](./examples/langchain_workflow.py)
- [`examples/openai_agents_workflow.py`](./examples/openai_agents_workflow.py)

## Layout

```
integrations/python/
├── examples/                              # runnable demos
├── tokenops_langchain/                    # PyPI: tokenops-langchain
│   ├── pyproject.toml
│   ├── README.md
│   └── tokenops_langchain/
│       ├── __init__.py
│       ├── callback.py                    # BaseCallbackHandler
│       └── env.py                         # configure_environment()
└── tokenops_openai_agents/                # PyPI: tokenops-openai-agents
    ├── pyproject.toml
    ├── README.md
    └── tokenops_openai_agents/
        ├── __init__.py
        ├── client.py                      # TokenOpsClient(AsyncOpenAI)
        └── env.py                         # configure_environment()
```

## Headers reference

| Header                   | Purpose                       |
|--------------------------|-------------------------------|
| `X-Tokenops-Workflow-Id` | Multi-step run grouping       |
| `X-Tokenops-Agent-Id`    | Which role / chain emitted    |
| `X-Tokenops-Session-Id`  | Conversation grouping         |
| `X-Tokenops-User-Id`     | End-user attribution          |
