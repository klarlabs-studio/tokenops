"""The attribution headers a model's client is given."""

import warnings

from tokenops_langchain.callback import TokenOpsCallback


def test_headers_carry_what_was_set() -> None:
    cb = TokenOpsCallback(workflow_id="wf", agent_id="a", user_id="u")
    assert cb.headers() == {
        "X-Tokenops-Workflow-Id": "wf",
        "X-Tokenops-Agent-Id": "a",
        "X-Tokenops-User-Id": "u",
    }


def test_new_run_gives_each_run_its_own_session() -> None:
    cb = TokenOpsCallback(workflow_id="wf", session_id="static")
    assert cb.headers()["X-Tokenops-Session-Id"] == "static"
    first = cb.new_run()
    assert cb.headers()["X-Tokenops-Session-Id"] == first
    assert cb.new_run() != first


def test_new_session_per_run_is_deprecated() -> None:
    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always")
        TokenOpsCallback(new_session_per_run=True)
    assert any(issubclass(w.category, DeprecationWarning) for w in caught)
