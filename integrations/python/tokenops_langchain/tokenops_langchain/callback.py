"""TokenOps attribution headers for LangChain models."""

from __future__ import annotations

import uuid
import warnings
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
    """TokenOps attribution headers for a LangChain model.

    The headers have to be set on the model's client: a callback cannot
    add them to the request. LangChain hands ``on_llm_start`` a copy of
    the invocation parameters, so headers written there never reach the
    SDK (an earlier version did that, and no request carried them).
    Pass ``headers()`` to the model instead::

        cb = TokenOpsCallback(workflow_id="research-summariser", agent_id="planner")
        llm = ChatOpenAI(model="gpt-4o-mini", default_headers=cb.headers())

    or per call, for a fresh session each run::

        cb.new_run()
        llm.bind(extra_headers=cb.headers()).invoke(...)

    Args:
        workflow_id: stable identifier shared across the whole run.
        agent_id: identifies the agent / chain / role emitting the call.
        session_id: optional grouping for conversation turns.
        user_id: optional end-user attribution.
        new_session_per_run: deprecated; it never reached a request. Call
            ``new_run()`` before taking ``headers()`` for a run instead.
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
        if new_session_per_run:
            warnings.warn(
                "new_session_per_run never reached a request; call new_run() and pass headers() "
                "to the model instead",
                DeprecationWarning,
                stacklevel=2,
            )
        self.workflow_id = workflow_id
        self.agent_id = agent_id
        self.session_id = session_id
        self.user_id = user_id
        self._current_session: Optional[str] = None

    def new_run(self) -> str:
        """Start a fresh session id for the next ``headers()``; returns it."""
        self._current_session = str(uuid.uuid4())
        return self._current_session

    def headers(self) -> dict[str, str]:
        """The attribution headers to set on the model's client."""
        out: dict[str, str] = {}
        if self.workflow_id:
            out[HEADER_WORKFLOW] = self.workflow_id
        if self.agent_id:
            out[HEADER_AGENT] = self.agent_id
        session = self._current_session or self.session_id
        if session:
            out[HEADER_SESSION] = session
        if self.user_id:
            out[HEADER_USER] = self.user_id
        return out
