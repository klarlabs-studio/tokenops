# tokenops-openai-agents

Drop-in TokenOps integration for the OpenAI Agents SDK. Subclasses
`AsyncOpenAI` so every request the Agents SDK makes routes through
the local TokenOps proxy with attribution headers pinned.

## Install

```bash
pip install tokenops-openai-agents
```

## Use

```python
import asyncio
from agents import Agent, Runner
from tokenops_openai_agents import TokenOpsClient

client = TokenOpsClient(
    workflow_id="invoice-extractor",
    agent_id="extractor",
)

agent = Agent(
    name="extractor",
    instructions="Extract structured fields from the input.",
    model="gpt-4o-mini",
)

async def main():
    result = await Runner.run(
        agent,
        input="Total: $1,200.00. Vendor: Acme.",
        openai_client=client,
    )
    print(result.final_output)

asyncio.run(main())
```

Every request reaching `api.openai.com` goes via TokenOps and carries:

```
X-Tokenops-Workflow-Id: invoice-extractor
X-Tokenops-Agent-Id: extractor
```

## Inspect

```bash
tokenops replay --workflow invoice-extractor
tokenops spend --by agent --top 5
```

## Configuration

| Argument         | Purpose                                              |
|------------------|------------------------------------------------------|
| `workflow_id`    | Stable workflow identifier                           |
| `agent_id`       | Identifier for the agent / role making the call      |
| `session_id`     | Optional grouping for conversation turns             |
| `user_id`        | Optional end-user attribution                        |
| `base_url`       | Override the proxy URL (default `http://127.0.0.1:7878/openai/v1`) |
| `default_headers`| Merged with the TokenOps headers (yours win conflicts) |

## Source

[github.com/felixgeelhaar/tokenops](https://github.com/felixgeelhaar/tokenops)
