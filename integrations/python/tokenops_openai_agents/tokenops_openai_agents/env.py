"""Helpers for pointing the OpenAI SDK at the TokenOps proxy."""

from __future__ import annotations

import os

DEFAULT_PROXY_HOST = "127.0.0.1:7878"


def configure_environment(
    proxy_host: str = DEFAULT_PROXY_HOST,
    *,
    overwrite: bool = False,
) -> str:
    """Set ``OPENAI_BASE_URL`` to the TokenOps proxy. Returns the value."""
    url = f"http://{proxy_host}/openai/v1"
    if "OPENAI_BASE_URL" in os.environ and not overwrite:
        return os.environ["OPENAI_BASE_URL"]
    os.environ["OPENAI_BASE_URL"] = url
    return url
