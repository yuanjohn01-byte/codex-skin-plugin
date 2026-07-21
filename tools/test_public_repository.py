#!/usr/bin/env python3
"""Positive and negative tests for the Public repository upload boundary."""

from __future__ import annotations

import hashlib
import json
import subprocess
import sys
import tempfile
from pathlib import Path

from validate_public_repo import forbidden_path_reason


ROOT = Path(__file__).resolve().parents[1]
VALIDATOR = ROOT / "tools" / "validate_public_repo.py"
LOCAL_ONLY_COMPONENTS = (
    ".claude",
    ".codex",
    ".codex-skin-local",
    ".history",
    ".idea",
    ".vscode",
    "archive",
    "archives",
    "artifact",
    "artifacts",
    "capture",
    "captures",
    "discussion",
    "discussions",
    "draft",
    "drafts",
    "evidence",
    "handoff",
    "handoffs",
    "log",
    "logs",
    "note",
    "notes",
    "one-time-handoff",
    "one_time_handoff",
    "output",
    "outputs",
    "personal",
    "prompts",
    "recording",
    "recordings",
    "scratch",
    "screenshot",
    "screenshots",
    "temp",
    "tmp",
    "transcript",
    "transcripts",
    "video",
    "videos",
)
MINIMAL_MANIFEST = {
    "name": "codex-skin",
    "version": "0.0.2",
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
MINIMAL_MARKETPLACE = {
    "name": "codex-skin",
    "interface": {"displayName": "Codex Skin"},
    "plugins": [
        {
            "name": "codex-skin",
            "source": {"source": "local", "path": "./plugins/codex-skin"},
            "policy": {"installation": "AVAILABLE", "authentication": "ON_INSTALL"},
            "category": "Developer Tools",
        }
    ],
}
VERSION_SKILL = """---
name: codex-skin-version
description: Report the installed Codex Skin v0.0.2 pre-release Plugin version and verify that its read-only test Skill loaded after installation or upgrade. Use for Codex Skin distribution checks only; this build cannot apply themes.
---

After the host loads this `SKILL.md`, do not call any additional tools, execute commands, access the network, or modify files or settings.
Plugin version: `0.0.2`.
Skill: `codex-skin-version`.
Upgrade target: replaces the v0.0.1 distribution-spike bundle.
Theme operations are not available in this test build.
"""
README_CONTRACT = """# Fixture
codex plugin marketplace add yuanjohn01-byte/codex-skin-plugin --ref main
codex plugin add codex-skin@codex-skin
codex plugin list --json
codex plugin marketplace upgrade codex-skin
codex plugin marketplace remove codex-skin
exactly one installed `codex-skin@codex-skin` entry
Completely quit Codex and reopen it in a new task.
Do not edit Codex configuration or delete Marketplace/Plugin cache directories.
If upgrade fails, leave it installed.
A post-merge two-platform check is required.
"""


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
    skill = fixture / "plugins" / "codex-skin" / "skills" / "codex-skin-version" / "SKILL.md"
    skill.parent.mkdir(parents=True)
    skill.write_text(VERSION_SKILL, encoding="utf-8")
    marketplace = fixture / ".agents" / "plugins" / "marketplace.json"
    marketplace.parent.mkdir(parents=True)
    marketplace.write_text(json.dumps(MINIMAL_MARKETPLACE), encoding="utf-8")
    (fixture / "LICENSE").write_text(
        "MIT License\nPermission is hereby granted\nTHE SOFTWARE IS PROVIDED \"AS IS\"\n",
        encoding="utf-8",
    )
    (fixture / "README.md").write_text(README_CONTRACT, encoding="utf-8")
    contracts = {
        "helper-protocol-v1.schema.json": {
            "$schema": "https://json-schema.org/draft/2020-12/schema",
            "$defs": {
                "progressEvent": {},
                "resultEvent": {},
                "error": {},
                "versionData": {},
                "doctorData": {},
            },
        },
        "helper-release-descriptor-v1.schema.json": {
            "$schema": "https://json-schema.org/draft/2020-12/schema",
            "required": [
                "schemaVersion",
                "helperVersion",
                "releaseTag",
                "publishedAt",
                "signingKeyId",
                "artifacts",
            ],
            "additionalProperties": False,
        },
        "device-authorization-poll-v1.schema.json": {
            "$schema": "https://json-schema.org/draft/2020-12/schema",
            "x-endpoints": {
                "poll": "/api/v1/plugin/device-authorizations/token",
                "cancel": "/api/v1/plugin/device-authorizations/cancel",
                "refresh": "/api/v1/plugin/token/refresh",
                "logout": "/api/v1/plugin/logout",
            },
            "$defs": {
                "proofRequest": {},
                "pollErrorEnvelope": {},
                "cancelSuccessEnvelope": {},
                "tokenSuccessEnvelope": {},
                "refreshRequest": {},
                "tokenErrorEnvelope": {},
                "logoutSuccessEnvelope": {},
                "logoutErrorEnvelope": {},
            },
        },
    }
    contract_root = fixture / "contracts"
    contract_root.mkdir(parents=True)
    manifest_artifacts = []
    for filename, payload in contracts.items():
        contract_bytes = (json.dumps(payload) + "\n").encode()
        (contract_root / filename).write_bytes(contract_bytes)
        manifest_artifacts.append(
            {
                "destination": f"contracts/{filename}",
                "sha256": hashlib.sha256(contract_bytes).hexdigest(),
                "source": f"codex-skin/contracts/public/{filename}",
            }
        )
    fixtures = {
        "fixtures/free-test-theme-v1/fixture-policy-v1.json": b"{}\n",
        "fixtures/free-test-theme-v1/fixture-provenance.json": b"{}\n",
        "fixtures/free-test-theme-v1/manifest.json": b"{}\n",
        "fixtures/free-test-theme-v1/assets/synthetic-dawn.png": b"fixture-png",
    }
    for destination, content in fixtures.items():
        target = fixture / destination
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(content)
        manifest_artifacts.append(
            {
                "destination": destination,
                "sha256": hashlib.sha256(content).hexdigest(),
                "source": f"codex-skin/fixtures/public/free-test-theme-v1/{Path(destination).relative_to('fixtures/free-test-theme-v1').as_posix()}",
            }
        )
    export_manifest = {
        "schemaVersion": 1,
        "generatedFrom": "codex-skin/contracts/public/export-allowlist.json",
        "artifacts": manifest_artifacts,
    }
    (fixture / "contracts" / "export-manifest.json").write_text(
        json.dumps(export_manifest), encoding="utf-8"
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


def negative_symlink(relative: str, directory: bool) -> None:
    with tempfile.TemporaryDirectory(prefix="codex-skin-public-boundary-") as directory_path:
        fixture = Path(directory_path)
        initialized = run(["git", "init", "--quiet"], fixture)
        if initialized.returncode != 0:
            raise AssertionError(initialized.stderr)
        write_baseline(fixture)
        source = fixture / ("docs/real" if directory else "src/real.txt")
        if directory:
            source.mkdir(parents=True)
            (source / "readme.md").write_text("safe\n", encoding="utf-8")
        else:
            source.parent.mkdir(parents=True, exist_ok=True)
            source.write_text("safe\n", encoding="utf-8")
        link = fixture / relative
        link.parent.mkdir(parents=True, exist_ok=True)
        link.symlink_to(source, target_is_directory=directory)
        added = run(["git", "add", "--force", "."], fixture)
        if added.returncode != 0:
            raise AssertionError(added.stderr)
        checked = run([sys.executable, str(VALIDATOR), "--root", str(fixture)], fixture)
        combined = checked.stdout + checked.stderr
        expected = f"symbolic links are not allowed in Public source: {relative}"
        if checked.returncode == 0 or expected not in combined:
            raise AssertionError(f"tracked symlink was not rejected:\n{combined}")


def negative_export_manifest(content: str, expected_message: str) -> None:
    with tempfile.TemporaryDirectory(prefix="codex-skin-public-export-") as directory:
        fixture = Path(directory)
        initialized = run(["git", "init", "--quiet"], fixture)
        if initialized.returncode != 0:
            raise AssertionError(initialized.stderr)
        write_baseline(fixture)
        (fixture / "contracts/export-manifest.json").write_text(content, encoding="utf-8")
        added = run(["git", "add", "--force", "."], fixture)
        if added.returncode != 0:
            raise AssertionError(added.stderr)
        checked = run([sys.executable, str(VALIDATOR), "--root", str(fixture)], fixture)
        combined = checked.stdout + checked.stderr
        if checked.returncode == 0 or expected_message not in combined:
            raise AssertionError(f"export manifest was not rejected:\n{combined}")


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


def negative_marketplace(payload: dict[str, object], expected_message: str) -> None:
    with tempfile.TemporaryDirectory(prefix="codex-skin-public-marketplace-") as directory:
        fixture = Path(directory)
        initialized = run(["git", "init", "--quiet"], fixture)
        if initialized.returncode != 0:
            raise AssertionError(initialized.stderr)
        write_baseline(fixture)
        marketplace = fixture / ".agents" / "plugins" / "marketplace.json"
        marketplace.write_text(json.dumps(payload), encoding="utf-8")
        added = run(["git", "add", "--force", "."], fixture)
        if added.returncode != 0:
            raise AssertionError(added.stderr)
        checked = run([sys.executable, str(VALIDATOR), "--root", str(fixture)], fixture)
        combined = checked.stdout + checked.stderr
        if checked.returncode == 0 or expected_message not in combined:
            raise AssertionError(f"validator accepted invalid marketplace metadata:\n{combined}")


def negative_skill(content: str, expected_message: str) -> None:
    with tempfile.TemporaryDirectory(prefix="codex-skin-public-skill-") as directory:
        fixture = Path(directory)
        initialized = run(["git", "init", "--quiet"], fixture)
        if initialized.returncode != 0:
            raise AssertionError(initialized.stderr)
        write_baseline(fixture)
        skill = fixture / "plugins" / "codex-skin" / "skills" / "codex-skin-version" / "SKILL.md"
        skill.write_text(content, encoding="utf-8")
        added = run(["git", "add", "--force", "."], fixture)
        if added.returncode != 0:
            raise AssertionError(added.stderr)
        checked = run([sys.executable, str(VALIDATOR), "--root", str(fixture)], fixture)
        combined = checked.stdout + checked.stderr
        if checked.returncode == 0 or expected_message not in combined:
            raise AssertionError(f"validator accepted invalid version Skill:\n{combined}")


def negative_readme(content: str, expected_message: str) -> None:
    with tempfile.TemporaryDirectory(prefix="codex-skin-public-readme-") as directory:
        fixture = Path(directory)
        initialized = run(["git", "init", "--quiet"], fixture)
        if initialized.returncode != 0:
            raise AssertionError(initialized.stderr)
        write_baseline(fixture)
        (fixture / "README.md").write_text(content, encoding="utf-8")
        added = run(["git", "add", "--force", "."], fixture)
        if added.returncode != 0:
            raise AssertionError(added.stderr)
        checked = run([sys.executable, str(VALIDATOR), "--root", str(fixture)], fixture)
        combined = checked.stdout + checked.stderr
        if checked.returncode == 0 or expected_message not in combined:
            raise AssertionError(f"validator accepted invalid README instructions:\n{combined}")


def main() -> int:
    current = run([sys.executable, str(VALIDATOR)], ROOT)
    if current.returncode != 0:
        sys.stderr.write(current.stdout + current.stderr)
        return 1

    local_only_examples = (
        ".env",
        ".dev.vars",
        ".claude/settings.json",
        "notes/private.md",
        "private/contract.json",
        "source-art/theme.psd",
        "themes/pro.cskin",
        "docs/internal/plan.md",
        "artifacts/helper.zip",
        "logs/ci.txt",
        "screenshots/review.png",
        "recordings/manual-test.mp4",
        "docs/drafts/plan.md",
        "docs/notes/review.md",
        "docs/temp/output.txt",
        "docs/logs/ci.txt",
        "docs/screenshots/review.png",
        "docs/recordings/test.mov",
        "docs/DrAfTs/mixed-case.md",
        "docs/handoffs/one-time.txt",
    )
    for relative in local_only_examples:
        assert_ignored(relative, True)
    for component in LOCAL_ONLY_COMPONENTS:
        relative = f"docs/{component}/sample.txt"
        assert_ignored(relative, True)
        if forbidden_path_reason(Path(relative)) is None:
            raise AssertionError(f"validator accepted nested local-only component: {relative}")

    for windows_or_case_variant in (
        r"docs\Drafts\plan.md",
        r"docs\NOTES\review.md",
        r"docs\Temp\output.txt",
        r"docs\LOGS\ci.txt",
        r"docs\ScreenShots\review.png",
        r"docs\Recordings\test.mov",
    ):
        if forbidden_path_reason(Path(windows_or_case_variant)) is None:
            raise AssertionError(
                f"validator accepted Windows/case local-only path: {windows_or_case_variant}"
            )

    for relative in (
        "AGENTS.md",
        "LICENSE",
        "plugins/codex-skin/assets/icon.png",
        "src/helper/main.go",
        "contracts/public/theme.schema.json",
        "tests/theme_test.go",
        "docs/user-installation.md",
        "SECURITY.md",
    ):
        assert_ignored(relative, False)
        if forbidden_path_reason(Path(relative)) is not None:
            raise AssertionError(f"validator rejected durable Public content: {relative}")

    negative_fixture(".env", b"EXAMPLE=value\n", "environment file")
    negative_fixture("notes/private.md", b"local note\n", "local-only evidence path")
    negative_fixture("docs/internal/plan.md", b"internal plan\n", "documentation path")
    negative_fixture("docs/planning/plan.md", b"internal plan\n", "documentation path")
    negative_fixture("docs/product/prd.md", b"internal PRD\n", "documentation path")
    negative_fixture(
        "docs/engineering/system-design.md", b"internal design\n", "documentation path"
    )
    negative_fixture("docs/operations/runbook.md", b"internal ops\n", "documentation path")
    negative_fixture("contracts/private/api.json", b"{}\n", "documentation path")
    negative_fixture("screenshots/review.png", b"raw capture\n", "local-only evidence path")
    negative_fixture("docs/drafts/plan.md", b"draft\n", "local-only evidence path")
    negative_fixture("docs/notes/review.md", b"note\n", "local-only evidence path")
    negative_fixture("docs/temp/output.txt", b"temporary\n", "local-only evidence path")
    negative_fixture("docs/logs/ci.txt", b"raw log\n", "local-only evidence path")
    negative_fixture(
        "docs/screenshots/review.png", b"capture\n", "local-only evidence path"
    )
    negative_fixture(
        "docs/recordings/test.mov", b"recording\n", "local-only evidence path"
    )
    marker = ("ship" + "any").encode("utf-8")
    negative_fixture("src/copied-template.txt", marker, "Private template marker")
    secret = b"access_" + b"token=\"" + b"sensitive-value-1234" + b"\"\n"
    negative_fixture("src/config.txt", secret, "generic-secret-assignment")
    negative_fixture("artifacts/helper.zip", b"binary", "local-only evidence path")
    negative_fixture("src/oversized.bin", b"0" * (5 * 1024 * 1024 + 1), "exceeds 5 MiB")
    negative_fixture(
        "plugins/codex-skin/.codex-plugin/notes.md",
        b"not a manifest\n",
        "only plugin.json belongs",
    )
    negative_symlink("src/file-link.txt", False)
    negative_symlink("docs/directory-link", True)

    with tempfile.TemporaryDirectory(prefix="codex-skin-public-export-source-") as directory:
        export_fixture = Path(directory)
        write_baseline(export_fixture)
        export_path = export_fixture / "contracts/export-manifest.json"
        export_manifest = json.loads(export_path.read_text(encoding="utf-8"))
        export_text = export_path.read_text(encoding="utf-8")
    negative_export_manifest(
        export_text.replace('"schemaVersion": 1,', '"schemaVersion": 1, "schemaVersion": 1,', 1),
        "duplicate JSON key",
    )
    for invalid_version in (True, 1.0, "1"):
        payload = dict(export_manifest)
        payload["schemaVersion"] = invalid_version
        negative_export_manifest(json.dumps(payload), "export manifest or SHA-256")
    nested = export_manifest["artifacts"][0]
    negative_export_manifest(
        export_text.replace(
            f'"destination": "{nested["destination"]}",',
            f'"destination": "{nested["destination"]}", "destination": "{nested["destination"]}",',
            1,
        ),
        "duplicate JSON key",
    )
    for mutate in (
        lambda payload: payload.__setitem__("artifacts", False),
        lambda payload: payload.__setitem__("artifacts", [False]),
        lambda payload: payload["artifacts"][0].__setitem__("destination", 1),
        lambda payload: payload["artifacts"][0].__setitem__("sha256", True),
        lambda payload: payload["artifacts"][0].__setitem__("extra", True),
        lambda payload: payload["artifacts"][0].pop("source"),
    ):
        payload = json.loads(export_text)
        mutate(payload)
        negative_export_manifest(json.dumps(payload), "export manifest or SHA-256")

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

    stale_version = dict(MINIMAL_MANIFEST)
    stale_version["version"] = "0.0.1"
    negative_manifest(stale_version, "version must be 0.0.2")

    invalid_homepage = dict(MINIMAL_MANIFEST)
    invalid_homepage["homepage"] = "http://example.invalid/plugin"
    negative_manifest(invalid_homepage, "homepage must be an absolute HTTPS URL")

    unsupported_component = dict(MINIMAL_MANIFEST)
    unsupported_component["mcpServers"] = "./.mcp.json"
    negative_manifest(unsupported_component, "field is not approved for the MVP")

    invalid_source = json.loads(json.dumps(MINIMAL_MARKETPLACE))
    invalid_source["plugins"][0]["source"]["path"] = "../private/codex-skin"
    negative_marketplace(invalid_source, "source must be local ./plugins/codex-skin")

    invalid_policy = json.loads(json.dumps(MINIMAL_MARKETPLACE))
    invalid_policy["plugins"][0]["policy"]["authentication"] = "ON_USE"
    negative_marketplace(invalid_policy, "policy must be AVAILABLE with ON_INSTALL")

    invalid_plugins = json.loads(json.dumps(MINIMAL_MARKETPLACE))
    invalid_plugins["plugins"] = []
    negative_marketplace(invalid_plugins, "must expose exactly one plugin entry")

    negative_skill(VERSION_SKILL.replace("name: codex-skin-version", "name: wrong-skill"), "frontmatter")
    negative_skill(VERSION_SKILL.replace("Plugin version: `0.0.2`.", "Plugin version: `9.9.9`."), "missing required marker")
    negative_fixture(
        "plugins/codex-skin/skills/extra/SKILL.md",
        b"---\nname: extra\ndescription: extra\n---\n",
        "may contain only the version check Skill",
    )
    negative_readme(
        README_CONTRACT.replace(" --ref main", " --ref codex/test-branch"),
        "non-canonical installation command",
    )
    negative_readme(
        README_CONTRACT.replace("codex plugin list --json", "codex plugin list --available"),
        "missing command: codex plugin list --json",
    )
    negative_readme(
        README_CONTRACT.replace(
            "Do not edit Codex configuration or delete Marketplace/Plugin cache directories.",
            "Delete the cache and edit config.toml.",
        ),
        "missing safety marker",
    )
    negative_fixture(
        "contracts/helper-protocol-v1.schema.json",
        b"{}\n",
        "export manifest or SHA-256",
    )
    negative_fixture(
        "contracts/helper-release-descriptor-v1.schema.json",
        b"{}\n",
        "export manifest or SHA-256",
    )
    negative_fixture(
        "contracts/device-authorization-poll-v1.schema.json",
        b"{}\n",
        "export manifest or SHA-256",
    )

    print("Public repository tests passed (positive scan + 41 negative fixtures).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
