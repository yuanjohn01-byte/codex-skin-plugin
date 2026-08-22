#!/usr/bin/env python3
"""Regression tests for the deterministic Helper release SBOM."""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
VERSION = "0.1.0-paid-alpha.15"


def summary() -> dict[str, object]:
    return {
        "schemaVersion": 1,
        "artifacts": [
            {
                "platform": platform,
                "helperVersion": VERSION,
                "filename": f"codex-skin-helper_{VERSION}_{suffix}",
                "sha256": digest * 64,
                "size": 123,
                "buildCommit": "a" * 40,
                "builtAt": "2026-08-06T00:00:00Z",
            }
            for platform, suffix, digest in (
                ("macos-arm64", "macos_arm64", "a"),
                ("macos-x64", "macos_x64", "b"),
                ("windows-x64", "windows_x64.exe", "c"),
            )
        ],
    }


def run(summary_path: Path, output: Path) -> bytes:
    subprocess.run(
        [sys.executable, "tools/create_sbom.py", "--summary", str(summary_path), "--output", str(output)],
        cwd=ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    return output.read_bytes()


def main() -> int:
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        source = root / "summary.json"
        source.write_text(json.dumps(summary()), encoding="utf-8")
        first = run(source, root / "first.json")
        second = run(source, root / "second.json")
        if first != second:
            raise AssertionError("SBOM output is not deterministic")
        payload = json.loads(first)
        if (
            payload.get("spdxVersion") != "SPDX-2.3"
            or payload.get("name") != f"codex-skin-helper-{VERSION}-sbom"
            or len(payload.get("files", [])) != 3
            or len(payload.get("packages", [])) < 2
        ):
            raise AssertionError(f"unexpected SBOM payload: {payload!r}")
    print("Helper SBOM tests passed (deterministic SPDX + 3 release binaries).")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
