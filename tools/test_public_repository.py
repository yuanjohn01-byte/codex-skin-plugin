#!/usr/bin/env python3
"""Positive and negative tests for the Public repository upload boundary."""

from __future__ import annotations

import subprocess
import sys
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
VALIDATOR = ROOT / "tools" / "validate_public_repo.py"
MINIMAL_MANIFEST = {
    "name": "codex-skin",
    "version": "0.0.0",
    "description": "fixture",
    "author": {"name": "fixture"},
    "interface": "fixture",
}


def run(command: list[str], cwd: Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run(command, cwd=cwd, check=False, capture_output=True, text=True)


def assert_ignored(relative: str, expected: bool) -> None:
    result = run(["git", "check-ignore", "--no-index", "--quiet", "--", relative], ROOT)
    ignored = result.returncode == 0
    if ignored != expected:
        expectation = "ignored" if expected else "trackable"
        raise AssertionError(f"expected {relative} to be {expectation}")


def write_baseline(fixture: Path) -> None:
    import json

    manifest = fixture / "plugins" / "codex-skin" / ".codex-plugin" / "plugin.json"
    manifest.parent.mkdir(parents=True, exist_ok=True)
    manifest.write_text(json.dumps(MINIMAL_MANIFEST), encoding="utf-8")
    (fixture / "LICENSE").write_text(
        "MIT License\nPermission is hereby granted\nTHE SOFTWARE IS PROVIDED \"AS IS\"\n",
        encoding="utf-8",
    )


def negative_fixture(relative: str, content: bytes, expected_message: str) -> None:
    with tempfile.TemporaryDirectory(prefix="codex-skin-public-boundary-") as directory:
        fixture = Path(directory)
        initialized = run(["git", "init", "--quiet"], fixture)
        if initialized.returncode != 0:
            raise AssertionError(initialized.stderr)
        write_baseline(fixture)
        target = fixture / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(content)
        added = run(["git", "add", "--force", "."], fixture)
        if added.returncode != 0:
            raise AssertionError(added.stderr)
        checked = run([sys.executable, str(VALIDATOR), "--root", str(fixture)], fixture)
        combined = checked.stdout + checked.stderr
        if checked.returncode == 0 or expected_message not in combined:
            raise AssertionError(f"validator did not reject {relative}:\n{combined}")


def main() -> int:
    current = run([sys.executable, str(VALIDATOR)], ROOT)
    if current.returncode != 0:
        sys.stderr.write(current.stdout + current.stderr)
        return 1

    for relative in (
        ".env",
        ".dev.vars",
        ".claude/settings.json",
        "notes/private.md",
        "private/contract.json",
        "source-art/theme.psd",
        "themes/pro.cskin",
        "docs/internal/plan.md",
        "artifacts/helper.zip",
    ):
        assert_ignored(relative, True)
    for relative in (
        "LICENSE",
        "plugins/codex-skin/assets/icon.png",
        "src/helper/main.go",
        "contracts/public/theme.schema.json",
        "tests/theme_test.go",
    ):
        assert_ignored(relative, False)

    negative_fixture(".env", b"EXAMPLE=value\n", "environment file")
    negative_fixture("notes/private.md", b"local note\n", "outside the Public allowlist")
    negative_fixture("docs/internal/plan.md", b"internal plan\n", "documentation path")
    marker = ("ship" + "any").encode("utf-8")
    negative_fixture("src/copied-template.txt", marker, "Private template marker")
    secret = b"access_" + b"token=\"" + b"sensitive-value-1234" + b"\"\n"
    negative_fixture("src/config.txt", secret, "generic-secret-assignment")
    negative_fixture("artifacts/helper.zip", b"binary", "outside the Public allowlist")
    negative_fixture("src/oversized.bin", b"0" * (5 * 1024 * 1024 + 1), "exceeds 5 MiB")

    print("Public repository boundary tests passed (positive scan + 7 negative fixtures).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
