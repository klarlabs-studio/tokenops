"""Tests for scripts/sec-gate.py.

Run: python3 -m unittest discover -s scripts -p 'test_*.py'
"""

from __future__ import annotations

import importlib.util
import os
import tempfile
import unittest
from pathlib import Path

_SPEC = importlib.util.spec_from_file_location("sec_gate", Path(__file__).with_name("sec-gate.py"))
assert _SPEC and _SPEC.loader
mod = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(mod)


class FindingsFileTest(unittest.TestCase):
    def setUp(self) -> None:
        self.dir = Path(tempfile.mkdtemp())
        self.root = self.dir / "findings.json"
        self.reports = self.dir / ".nox" / "reports" / "findings.json"
        self.candidates = (self.root, self.reports)

    def write(self, path: Path, mtime: int) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("{}")
        os.utime(path, (mtime, mtime))

    def test_none_written_names_the_first(self) -> None:
        self.assertEqual(mod.findings_file(self.candidates), self.root)

    def test_older_nox_writes_the_working_directory(self) -> None:
        self.write(self.root, 100)
        self.assertEqual(mod.findings_file(self.candidates), self.root)

    def test_newer_nox_writes_its_reports_directory(self) -> None:
        self.write(self.reports, 100)
        self.assertEqual(mod.findings_file(self.candidates), self.reports)

    def test_the_newest_scan_wins_over_a_stale_file(self) -> None:
        self.write(self.root, 100)
        self.write(self.reports, 200)
        self.assertEqual(mod.findings_file(self.candidates), self.reports)
        self.write(self.root, 300)
        self.assertEqual(mod.findings_file(self.candidates), self.root)


if __name__ == "__main__":
    unittest.main()
