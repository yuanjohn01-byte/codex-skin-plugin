#!/usr/bin/env python3
"""Positive and negative tests for the Public repository upload boundary."""

from __future__ import annotations

import json
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
    "author": {"name": "fixture", "url": "https://example.invalid/author"},
    "homepage": "https://example.invalid/plugin",
    "repository": "https://github.com/yuanjohn01-byte/codex-skin-plugin",
    "license": "MIT",
    "keywords": ["fixture"],
    "skills": "./skills/",
    "interface": {
        "displayName": "Fixture",
        "shortDescription": "Fixture plugin",
        "longDescription": "Fixture plugin for repository validation.",
        "developerName": "Fixture",
        "category": "Developer Tools",
        "capabilities": ["Fixture"],
        "websiteURL": "https://example.invalid",
        "defaultPrompt": ["Run the fixture."],
    },
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
    manifest = fixture / "plugins" / "codex-skin" / ".codex-plugin" / "plugin.json"
    manifest.parent.mkdir(parents=True, exist_ok=True)
    manifest.write_text(json.dumps(MINIMAL_MANIFEST), encoding="utf-8")
    (fixture / "plugins" / "codex-skin" / "skills").mkdir(parents=True)
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


def negative_manifest(payload: dict[str, object], expected_message: str) -> None:
    with tempfile.TemporaryDirectory(prefix="codex-skin-public-manifest-") as directory:
        fixture = Path(directory)
        initialized = run(["git", "init", "--quiet"], fixture)
        if initialized.returncode != 0:
            raise AssertionError(initialized.stderr)
        write_baseline(fixture)
        manifest = fixture / "plugins" / "codex-skin" / ".codex-plugin" / "plugin.json"
        manifest.write_text(json.dumps(payload), encoding="utf-8")
        added = run(["git", "add", "--force", "."], fixture)
        if added.returncode != 0:
            raise AssertionError(added.stderr)
        checked = run([sys.executable, str(VALIDATOR), "--root", str(fixture)], fixture)
        combined = checked.stdout + checked.stderr
        if checked.returncode == 0 or expected_message not in combined:
            raise AssertionError(f"validator accepted an invalid manifest:\n{combined}")


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
    negative_fixture(
        "plugins/codex-skin/.codex-plugin/notes.md",
        b"not a manifest\n",
        "only plugin.json belongs",
    )

    invalid_name = dict(MINIMAL_MANIFEST)
    invalid_name["name"] = "Codex Skin"
    negative_manifest(invalid_name, "name must be codex-skin")

    invalid_skills = dict(MINIMAL_MANIFEST)
    invalid_skills["skills"] = "../private-skills"
    negative_manifest(invalid_skills, "skills must be a ./-prefixed path")

    invalid_interface = dict(MINIMAL_MANIFEST)
    invalid_interface["interface"] = "fixture"
    negative_manifest(invalid_interface, "interface must be an object")

    invalid_version = dict(MINIMAL_MANIFEST)
    invalid_version["version"] = "00.1.0"
    negative_manifest(invalid_version, "version must be strict semver")

    invalid_homepage = dict(MINIMAL_MANIFEST)
    invalid_homepage["homepage"] = "http://example.invalid/plugin"
    negative_manifest(invalid_homepage, "homepage must be an absolute HTTPS URL")

    unsupported_component = dict(MINIMAL_MANIFEST)
    unsupported_component["mcpServers"] = "./.mcp.json"
    negative_manifest(unsupported_component, "field is not approved for the MVP")

    print("Public repository tests passed (positive scan + 14 negative fixtures).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
