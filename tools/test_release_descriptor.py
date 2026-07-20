#!/usr/bin/env python3
"""Deterministic and negative tests for release descriptor generation."""

from __future__ import annotations

import copy
import json
import subprocess
import sys
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
GENERATOR = ROOT / "tools" / "create_release_descriptor.py"
PLATFORMS = ("macos-arm64", "macos-x64", "windows-x64")


def fixture() -> dict[str, object]:
    suffixes = ("macos_arm64", "macos_x64", "windows_x64.exe")
    return {
        "schemaVersion": 1,
        "artifacts": [
            {
                "platform": platform,
                "filename": f"codex-skin-helper_0.1.0-s3_{suffix}",
                "helperVersion": "0.1.0-s3",
                "builtAt": "2026-07-20T08:00:00+08:00",
                "sha256": str(index + 1) * 64,
                "size": 1_900_000 + index,
            }
            for index, (platform, suffix) in enumerate(zip(PLATFORMS, suffixes, strict=True))
        ],
    }


def run(summary: dict[str, object], directory: Path) -> subprocess.CompletedProcess[str]:
    summary_path = directory / "build-summary.json"
    output_path = directory / "release-descriptor.json"
    summary_path.write_text(json.dumps(summary), encoding="utf-8")
    return subprocess.run(
        [
            sys.executable,
            str(GENERATOR),
            "--summary",
            str(summary_path),
            "--output",
            str(output_path),
            "--key-id",
            "internal-spike-2026-01",
        ],
        cwd=ROOT,
        check=False,
        capture_output=True,
        text=True,
    )


def main() -> int:
    with tempfile.TemporaryDirectory(prefix="codex-skin-release-descriptor-") as raw_directory:
        directory = Path(raw_directory)
        result = run(fixture(), directory)
        if result.returncode != 0:
            raise AssertionError(result.stdout + result.stderr)
        first = (directory / "release-descriptor.json").read_bytes()
        result = run(fixture(), directory)
        if result.returncode != 0:
            raise AssertionError(result.stdout + result.stderr)
        second = (directory / "release-descriptor.json").read_bytes()
        if first != second or not first.endswith(b"\n") or b" " in first:
            raise AssertionError("release descriptor is not deterministic canonical JSON")
        descriptor = json.loads(first)
        if descriptor["publishedAt"] != "2026-07-20T00:00:00Z":
            raise AssertionError("release timestamp was not normalized to UTC")
        if [item["platform"] for item in descriptor["artifacts"]] != list(PLATFORMS):
            raise AssertionError("release platforms are not in the fixed order")

        duplicate = copy.deepcopy(fixture())
        duplicate["artifacts"][1]["platform"] = "macos-arm64"
        if run(duplicate, directory).returncode == 0:
            raise AssertionError("duplicate platform was accepted")

        bad_digest = copy.deepcopy(fixture())
        bad_digest["artifacts"][0]["sha256"] = "A" * 64
        if run(bad_digest, directory).returncode == 0:
            raise AssertionError("noncanonical digest was accepted")

        bad_filename = copy.deepcopy(fixture())
        bad_filename["artifacts"][2]["filename"] = "../helper.exe"
        if run(bad_filename, directory).returncode == 0:
            raise AssertionError("unsafe artifact filename was accepted")

    print("Release descriptor generator tests passed (deterministic + 3 negative cases).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
