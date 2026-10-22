"""Tests for scripts/sarif-apply-vex.py.

Run: python3 -m unittest discover -s scripts -p 'test_*.py'
"""

from __future__ import annotations

import importlib.util
import io
import json
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path

_SPEC = importlib.util.spec_from_file_location(
    "sarif_apply_vex", Path(__file__).with_name("sarif-apply-vex.py")
)
assert _SPEC and _SPEC.loader
mod = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(mod)

FP_WAIVED = "a" * 64
FP_SAME_RULE = "b" * 64
FP_OTHER = "c" * 64


def result(rule: str, fp: str) -> dict:
    return {
        "ruleId": rule,
        "level": "warning",
        "message": {"text": rule},
        "locations": [{"physicalLocation": {"artifactLocation": {"uri": "x.go"},
                                            "region": {"startLine": 1}}}],
        "fingerprints": {"nox/v1": fp},
    }


def sarif(*results: dict) -> dict:
    return {
        "version": "2.1.0",
        "runs": [{"tool": {"driver": {"name": "nox", "rules": []}},
                  "results": list(results)}],
    }


def statement(rule: str, fp: str, status: str = "not_affected") -> dict:
    return {"vulnerability": rule, "status": status, "_nox_fingerprint": fp}


class ApplyVEXTest(unittest.TestCase):
    def setUp(self) -> None:
        self.dir = Path(tempfile.mkdtemp())

    def run_main(self, doc: dict, vex: dict | None) -> tuple[int, dict]:
        src = self.dir / "results.sarif"
        out = self.dir / "nox.sarif"
        vex_path = self.dir / "vex.json"
        src.write_text(json.dumps(doc))
        if vex is not None:
            vex_path.write_text(json.dumps(vex))
        with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
            code = mod.main([str(src), str(vex_path), str(out)])
        return code, (json.loads(out.read_text()) if out.exists() else {})

    def test_drops_only_the_waived_fingerprint_not_the_whole_rule(self) -> None:
        # nox's own --vex matches by rule ID, so one AI-006 waiver hides
        # every AI-006 finding. The upload must bind to the fingerprint.
        code, out = self.run_main(
            sarif(result("AI-006", FP_WAIVED), result("AI-006", FP_SAME_RULE),
                  result("SEC-041", FP_OTHER)),
            {"statements": [statement("AI-006", FP_WAIVED)]},
        )
        self.assertEqual(code, 0)
        kept = [r["fingerprints"]["nox/v1"] for r in out["runs"][0]["results"]]
        self.assertEqual(kept, [FP_SAME_RULE, FP_OTHER])

    def test_statements_that_are_not_not_affected_waive_nothing(self) -> None:
        _, out = self.run_main(
            sarif(result("AI-006", FP_WAIVED)),
            {"statements": [statement("AI-006", FP_WAIVED, "under_investigation")]},
        )
        self.assertEqual(len(out["runs"][0]["results"]), 1)

    def test_absent_vex_keeps_every_result(self) -> None:
        code, out = self.run_main(sarif(result("AI-006", FP_WAIVED)), None)
        self.assertEqual(code, 0)
        self.assertEqual(len(out["runs"][0]["results"]), 1)

    def test_missing_sarif_fails_loudly(self) -> None:
        with redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()):
            code = mod.main([str(self.dir / "absent.sarif"), str(self.dir / "vex.json"),
                             str(self.dir / "out.sarif")])
        self.assertNotEqual(code, 0)

    def test_repository_vex_parses_and_every_waiver_has_a_fingerprint(self) -> None:
        vex = Path(__file__).resolve().parent.parent / "security" / "vex.json"
        if not vex.exists():
            self.skipTest("security/vex.json absent")
        waived = mod.waived_fingerprints(vex)
        statements = json.loads(vex.read_text())["statements"]
        self.assertEqual(len(waived), len(statements),
                         "every statement needs a distinct _nox_fingerprint")


if __name__ == "__main__":
    unittest.main()
