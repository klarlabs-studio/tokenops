# tokenops-langchain

LangChain callback handler that pairs with the TokenOps proxy:
emits attribution headers on every LLM call, configures the SDK base
URLs, and surfaces TokenOps events alongside LangChain runs.

## Install

```bash
pip install tokenops-langchain
```

## Use

```python
from langchain_openai import ChatOpenAI
from langchain_core.prompts import ChatPromptTemplate
from tokenops_langchain import TokenOpsCallback, configure_environment

# Point OpenAI / Anthropic / Gemini SDKs at the local TokenOps proxy.
configure_environment()

callback = TokenOpsCallback(
    workflow_id="research-summariser",
    agent_id="planner",
    new_session_per_run=True,
)

llm = ChatOpenAI(model="gpt-4o-mini", callbacks=[callback])
prompt = ChatPromptTemplate.from_messages(
    [("system", "Be brief."), ("user", "{question}")]
)
chain = prompt | llm
chain.invoke({"question": "What is the speed of light?"})
```

Every request that flows through the proxy will carry:

```
X-Tokenops-Workflow-Id: research-summariser
X-Tokenops-Agent-Id: planner
X-Tokenops-Session-Id: <fresh uuid per chain run>
```

The TokenOps daemon stitches these into workflow traces visible
through `tokenops replay --workflow research-summariser`.

## Configuration

| Argument               | Purpose                                       |
|------------------------|-----------------------------------------------|
| `workflow_id`          | Stable identifier across the run              |
| `agent_id`             | Which chain / role made the call              |
| `session_id`           | Static session grouping                       |
| `user_id`              | End-user attribution                          |
| `new_session_per_run`  | Generate a fresh UUID per chain run           |

`configure_environment()` accepts a `proxy_host` (default
`127.0.0.1:7878`) and a list of providers; pass `overwrite=True` to
force-set when the env var already exists.

## Source

[github.com/felixgeelhaar/tokenops](https://github.com/felixgeelhaar/tokenops)
