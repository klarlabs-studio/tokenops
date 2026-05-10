"""TokenOps middleware for the OpenAI Agents SDK.

Provides:
- ``TokenOpsClient`` — a thin ``openai.AsyncOpenAI`` subclass with
  TokenOps attribution headers pinned to ``default_headers`` so every
  request the Agents SDK makes is stitched into the right workflow.
- ``configure_environment`` — set ``OPENAI_BASE_URL`` to the local
  proxy without forcing the caller to remember the path.
"""

from .client import TokenOpsClient
from .env import configure_environment

__all__ = ["TokenOpsClient", "configure_environment"]
__version__ = "0.1.0"
