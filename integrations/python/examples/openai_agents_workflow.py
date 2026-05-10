"""End-to-end example: OpenAI Agents SDK routed through TokenOps."""

import asyncio

from agents import Agent, Runner

from tokenops_openai_agents import TokenOpsClient


async def main() -> None:
    client = TokenOpsClient(
        workflow_id="demo-extractor",
        agent_id="extractor",
    )
    agent = Agent(
        name="extractor",
        instructions="Extract the dollar amount from the input. Reply with a number.",
        model="gpt-4o-mini",
    )
    result = await Runner.run(
        agent,
        input="Total invoice value: $1,234.56. Customer: Acme.",
        openai_client=client,
    )
    print("agent:", result.final_output)


if __name__ == "__main__":
    asyncio.run(main())
