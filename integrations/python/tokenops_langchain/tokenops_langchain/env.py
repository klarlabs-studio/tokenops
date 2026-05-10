"""Helpers for pointing client SDKs at the local TokenOps proxy."""

from __future__ import annotations

import os
from typing import Mapping, Optional

DEFAULT_PROXY_HOST = "127.0.0.1:7878"

PROVIDER_ENV: Mapping[str, str] = {
    "openai": "OPENAI_BASE_URL",
    "anthropic": "ANTHROPIC_BASE_URL",
    "gemini": "GOOGLE_GENAI_HTTP_OPTIONS_BASE_URL",
}

PROVIDER_PATH: Mapping[str, str] = {
    "openai": "/openai/v1",
    "anthropic": "/anthropic",
    "gemini": "/gemini",
}


def configure_environment(
    proxy_host: str = DEFAULT_PROXY_HOST,
    providers: Optional[list[str]] = None,
    *,
    overwrite: bool = False,
) -> dict[str, str]:
    """Set the standard ``*_BASE_URL`` env vars so SDKs route through TokenOps.

    Returns a dict of the env vars that were set so callers can log /
    assert in tests. ``overwrite=False`` (the default) leaves existing
    env values alone; pass ``True`` to force-set.
    """
    selected = providers or list(PROVIDER_ENV)
    applied: dict[str, str] = {}
    for provider in selected:
        env_var = PROVIDER_ENV[provider]
        if env_var in os.environ and not overwrite:
            continue
        url = f"http://{proxy_host}{PROVIDER_PATH[provider]}"
        os.environ[env_var] = url
        applied[env_var] = url
    return applied
