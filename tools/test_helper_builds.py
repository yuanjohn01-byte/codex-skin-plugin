#!/usr/bin/env python3
"""Reproducibility and format checks for Helper cross-build artifacts."""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
BUILDER = ROOT / "tools" / "build_helper.py"


def build(output: Path) -> dict[str, object]:
    result = subprocess.run(
        [
            sys.executable,
            str(BUILDER),
            "--output",
            str(output),
            "--release-profile",
            "production",
        ],
        cwd=ROOT,
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        raise AssertionError(result.stdout + result.stderr)
    return json.loads((output / "build-summary.json").read_text(encoding="utf-8"))


def main() -> int:
    with tempfile.TemporaryDirectory(prefix="codex-skin-helper-builds-") as directory:
        root = Path(directory)
        first = build(root / "first")
        second = build(root / "second")
        if first != second:
            raise AssertionError("repeated Helper builds produced different summaries")

        artifacts = first.get("artifacts")
        if (
            first.get("releaseProfile") != "production"
            or first.get("apiBaseURL") != "https://codexskin.ai"
        ):
            raise AssertionError("Helper build did not use the fixed Production profile")
        if not isinstance(artifacts, list) or len(artifacts) != 3:
            raise AssertionError("expected exactly three Helper test artifacts")
        platforms = {item.get("platform") for item in artifacts if isinstance(item, dict)}
        if platforms != {"macos-arm64", "macos-x64", "windows-x64"}:
            raise AssertionError(f"unexpected Helper target set: {platforms}")
        if any(item.get("cgoEnabled") is not False for item in artifacts if isinstance(item, dict)):
            raise AssertionError("Helper test artifacts must use CGO_ENABLED=0")
        if any(
            item.get("helperVersion") != "0.1.0-paid-alpha.17"
            for item in artifacts
            if isinstance(item, dict)
        ):
            raise AssertionError("Helper test artifacts did not use immutable Production version .17")
        if any(
            item.get("helperReleaseTag") != "helper-v0.1.0-paid-alpha.17"
            for item in artifacts
            if isinstance(item, dict)
        ):
            raise AssertionError("Helper test artifacts did not bind the Production release tag")

    print(
        "Production Helper cross-build tests passed (fixed origin/version; 3 targets; repeatable SHA-256 and formats)."
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
