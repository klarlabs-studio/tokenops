# tokenops-langchain

Attribution headers for LangChain models that call through the
TokenOps proxy, and the SDK base URLs that point them at it.

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

attribution = TokenOpsCallback(workflow_id="research-summariser", agent_id="planner")
attribution.new_run()  # a fresh session id for this run

# The headers go on the model's client: a LangChain callback cannot add
# headers to a request.
llm = ChatOpenAI(model="gpt-4o-mini", default_headers=attribution.headers())
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
X-Tokenops-Session-Id: <the uuid new_run() made>
```

The TokenOps daemon stitches these into workflow traces visible
through `tokenops replay --workflow-id research-summariser`.

For a fresh session on each call without a new model, bind the
headers per call:

```python
attribution.new_run()
llm.bind(extra_headers=attribution.headers()).invoke("...")
```

Versions before this one set the headers from a callback
(`callbacks=[TokenOpsCallback(...)]`). LangChain hands callbacks a copy
of the request parameters, so no request carried them.

## Configuration

| Argument               | Purpose                                       |
|------------------------|-----------------------------------------------|
| `workflow_id`          | Stable identifier across the run              |
| `agent_id`             | Which chain / role made the call              |
| `session_id`           | Static session grouping                       |
| `user_id`              | End-user attribution                          |
| `new_session_per_run`  | Deprecated: call `new_run()` instead          |

`configure_environment()` accepts a `proxy_host` (default
`127.0.0.1:7878`) and a list of providers; pass `overwrite=True` to
force-set when the env var already exists.

## Source

[github.com/klarlabs-studio/tokenops](https://github.com/klarlabs-studio/tokenops)
