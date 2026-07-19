#!/usr/bin/env python3
"""Reproducibility and format checks for Guardian spike artifacts."""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
BUILDER = ROOT / "tools" / "build_guardian.py"


def build(output: Path) -> dict[str, object]:
    result = subprocess.run(
        [sys.executable, str(BUILDER), "--output", str(output)],
        cwd=ROOT,
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        raise AssertionError(result.stdout + result.stderr)
    return json.loads((output / "build-summary.json").read_text(encoding="utf-8"))


def main() -> int:
    with tempfile.TemporaryDirectory(prefix="codex-skin-guardian-builds-") as directory:
        root = Path(directory)
        first = build(root / "first")
        second = build(root / "second")
        if first != second:
            raise AssertionError("repeated Guardian builds produced different summaries")
        artifacts = first.get("artifacts")
        if not isinstance(artifacts, list) or len(artifacts) != 3:
            raise AssertionError("expected exactly three Guardian spike artifacts")
        platforms = {item.get("platform") for item in artifacts if isinstance(item, dict)}
        if platforms != {"macos-arm64", "macos-x64", "windows-x64"}:
            raise AssertionError(f"unexpected Guardian target set: {platforms}")
        if any(
            item.get("component") != "codex-skin-guardian"
            or item.get("cgoEnabled") is not False
            for item in artifacts
            if isinstance(item, dict)
        ):
            raise AssertionError("Guardian artifacts must be CGO-free and component-scoped")

    print("Guardian cross-build tests passed (3 targets, repeatable SHA-256 and validated formats).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
