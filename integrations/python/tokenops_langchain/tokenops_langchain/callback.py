"""LangChain callback that emits TokenOps attribution headers."""

from __future__ import annotations

import uuid
from typing import Any, Optional

try:
    from langchain_core.callbacks.base import BaseCallbackHandler
except ImportError:  # pragma: no cover - optional import
    BaseCallbackHandler = object  # type: ignore[assignment, misc]


HEADER_WORKFLOW = "X-Tokenops-Workflow-Id"
HEADER_AGENT = "X-Tokenops-Agent-Id"
HEADER_SESSION = "X-Tokenops-Session-Id"
HEADER_USER = "X-Tokenops-User-Id"


class TokenOpsCallback(BaseCallbackHandler):
    """Stamp every LLM invocation with TokenOps attribution headers.

    The callback mutates the ``extra_headers`` slot LangChain passes to
    the underlying SDK request. The proxy reads these headers to stitch
    multi-step runs into workflows.

    Args:
        workflow_id: stable identifier shared across the whole run.
        agent_id: identifies the agent / chain / role emitting the call.
        session_id: optional grouping for conversation turns.
        user_id: optional end-user attribution.
        new_session_per_run: when True (default False), generate a fresh
            ``session_id`` on each chain run so distinct interactions do
            not get conflated. Useful for stateless services.
    """

    def __init__(
        self,
        *,
        workflow_id: Optional[str] = None,
        agent_id: Optional[str] = None,
        session_id: Optional[str] = None,
        user_id: Optional[str] = None,
        new_session_per_run: bool = False,
    ) -> None:
        super().__init__()
        self.workflow_id = workflow_id
        self.agent_id = agent_id
        self.session_id = session_id
        self.user_id = user_id
        self.new_session_per_run = new_session_per_run
        self._current_session: Optional[str] = None

    # LangChain calls this hook before each chain run; it is the only
    # opportunity we have to refresh per-run state before LLM calls fire.
    def on_chain_start(self, *args: Any, **kwargs: Any) -> None:
        if self.new_session_per_run:
            self._current_session = str(uuid.uuid4())

    # LangChain plumbs ``invocation_params`` through to the underlying
    # SDK as ``extra_headers``. We merge our headers with whatever the
    # caller already set so attribution does not clobber other flags.
    def on_llm_start(
        self,
        serialized: Any,
        prompts: Any,
        *,
        invocation_params: Optional[dict[str, Any]] = None,
        **kwargs: Any,
    ) -> None:
        if invocation_params is None:
            return
        headers = invocation_params.setdefault("extra_headers", {})
        if self.workflow_id:
            headers.setdefault(HEADER_WORKFLOW, self.workflow_id)
        if self.agent_id:
            headers.setdefault(HEADER_AGENT, self.agent_id)
        session = self._current_session or self.session_id
        if session:
            headers.setdefault(HEADER_SESSION, session)
        if self.user_id:
            headers.setdefault(HEADER_USER, self.user_id)

    # The default implementations of the remaining BaseCallbackHandler
    # hooks are no-ops; we keep the class focused on header emission so
    # the audit story stays simple.
