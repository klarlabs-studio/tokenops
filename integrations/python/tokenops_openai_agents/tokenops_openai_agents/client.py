"""TokenOps-aware ``AsyncOpenAI`` client for the OpenAI Agents SDK."""

from __future__ import annotations

from typing import Any, Optional

from openai import AsyncOpenAI

HEADER_WORKFLOW = "X-Tokenops-Workflow-Id"
HEADER_AGENT = "X-Tokenops-Agent-Id"
HEADER_SESSION = "X-Tokenops-Session-Id"
HEADER_USER = "X-Tokenops-User-Id"


class TokenOpsClient(AsyncOpenAI):
    """``AsyncOpenAI`` with TokenOps attribution headers pre-applied.

    Pass an instance to ``Agent(..., client=TokenOpsClient(...))`` (or
    set it on a ``Runner`` config) so every request the Agents SDK makes
    inherits the right TokenOps attribution. The base URL defaults to
    the local proxy; pass ``base_url`` explicitly if you've moved it.

    Example::

        from agents import Agent, Runner
        from tokenops_openai_agents import TokenOpsClient

        client = TokenOpsClient(
            workflow_id="invoice-extractor",
            agent_id="extractor",
        )
        agent = Agent(name="extractor", instructions="...", model="gpt-4o-mini")
        result = await Runner.run(agent, input="extract from this PDF...", openai_client=client)
    """

    def __init__(
        self,
        *,
        workflow_id: Optional[str] = None,
        agent_id: Optional[str] = None,
        session_id: Optional[str] = None,
        user_id: Optional[str] = None,
        base_url: Optional[str] = None,
        default_headers: Optional[dict[str, str]] = None,
        **kwargs: Any,
    ) -> None:
        merged: dict[str, str] = {}
        if default_headers:
            merged.update(default_headers)
        if workflow_id:
            merged[HEADER_WORKFLOW] = workflow_id
        if agent_id:
            merged[HEADER_AGENT] = agent_id
        if session_id:
            merged[HEADER_SESSION] = session_id
        if user_id:
            merged[HEADER_USER] = user_id

        if base_url is None:
            base_url = "http://127.0.0.1:7878/openai/v1"

        super().__init__(
            base_url=base_url,
            default_headers=merged,
            **kwargs,
        )
