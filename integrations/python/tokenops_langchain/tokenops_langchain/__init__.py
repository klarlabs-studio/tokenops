"""TokenOps LangChain integration.

Two pieces:
- ``TokenOpsCallback`` — a LangChain ``BaseCallbackHandler`` that stamps
  every chat-model invocation with TokenOps attribution headers
  (workflow / agent / session / user) so the proxy can stitch related
  calls into workflows.
- ``configure_environment`` — a tiny helper that points the OpenAI /
  Anthropic SDKs at the local TokenOps proxy by setting the standard
  ``*_BASE_URL`` env vars. No SDK fork; LangChain consumes the same
  SDKs underneath.

Typical usage::

    from langchain_openai import ChatOpenAI
    from tokenops_langchain import TokenOpsCallback, configure_environment

    configure_environment()  # OPENAI_BASE_URL=http://127.0.0.1:7878/openai/v1

    llm = ChatOpenAI(model="gpt-4o-mini", callbacks=[TokenOpsCallback(
        workflow_id="research-summariser",
        agent_id="planner",
    )])
"""

from .callback import TokenOpsCallback
from .env import configure_environment

__all__ = ["TokenOpsCallback", "configure_environment"]
__version__ = "0.1.0"
