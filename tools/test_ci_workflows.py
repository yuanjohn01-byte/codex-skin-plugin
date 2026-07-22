#!/usr/bin/env python3
"""Prevent duplicate feature-push and pull-request workflow executions."""

from __future__ import annotations

import fnmatch
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
WORKFLOWS = ROOT / ".github" / "workflows"
BASELINE = WORKFLOWS / "ci.yml"


def pull_request_paths(path: Path) -> set[str]:
    """Read only the quoted pull_request.paths entries from one workflow."""
    paths: set[str] = set()
    in_pull_request = False
    in_paths = False
    for line in path.read_text().splitlines():
        if line == "  pull_request:":
            in_pull_request = True
            in_paths = False
            continue
        if in_pull_request and line.startswith("  ") and not line.startswith("    "):
            break
        if in_pull_request and line == "    paths:":
            in_paths = True
            continue
        if in_paths and line.startswith("      - "):
            paths.add(line.removeprefix("      - ").strip().strip("\"'"))
        elif in_paths and line.strip():
            break
    return paths


def main() -> int:
    workflow_paths = sorted(WORKFLOWS.glob("*.yml"))
    if not workflow_paths:
        raise AssertionError("no Public workflows found")

    for path in workflow_paths:
        workflow = path.read_text()
        if "codex/**" in workflow:
            raise AssertionError(f"{path.name} restores duplicate feature push CI")
        for marker in ("pull_request:", "workflow_dispatch:", "concurrency:"):
            if marker not in workflow:
                raise AssertionError(f"{path.name} is missing {marker}")
        if "cancel-in-progress: true" not in workflow:
            raise AssertionError(f"{path.name} does not cancel stale runs")
        for forbidden in (
            "yuanjohn01-byte/codex-skin.git",
            "repository: yuanjohn01-byte/codex-skin",
            "gh api repos/yuanjohn01-byte/codex-skin",
        ):
            if forbidden in workflow:
                raise AssertionError(f"{path.name} makes Public baseline depend on Private")

    baseline = BASELINE.read_text()
    if "  push:" not in baseline or "      - main" not in baseline:
        raise AssertionError("Public baseline must verify merged main")
    if "paths:" in baseline or "paths-ignore:" in baseline:
        raise AssertionError("Public baseline must remain the independent always-on PR gate")
    for marker in (
        "repository-boundary:",
        "fetch-depth: 0",
        "tools/ci_scope.py",
        "run_fixture",
        "run_go",
        "lightweight_main",
        "if: steps.scope.outputs.run_go == 'true'",
    ):
        if marker not in baseline:
            raise AssertionError(f"Public baseline lost component routing marker: {marker}")
    if baseline.count("actions/setup-go@v5") != 1:
        raise AssertionError("Public baseline must set up Go at most once")

    for path in workflow_paths:
        if path == BASELINE:
            continue
        workflow = path.read_text()
        if "  push:" in workflow:
            raise AssertionError(f"{path.name} must not run on feature or main push")
        if "    paths:" not in workflow:
            raise AssertionError(f"{path.name} must remain path-scoped on PRs")
        if "README.md" in workflow or "AGENTS.md" in workflow:
            raise AssertionError(f"{path.name} runs a platform matrix for docs-only changes")

    windows_plugin = (WORKFLOWS / "windows-plugin-spike.yml").read_text()
    if "'tools/**'" in windows_plugin or '"tools/**"' in windows_plugin:
        raise AssertionError("Windows Plugin smoke must not run for every governance tool change")
    for marker in (
        ".gitattributes",
        ".agents/plugins/marketplace.json",
        "plugins/codex-skin/**",
        ".github/workflows/windows-plugin-spike.yml",
    ):
        if marker not in windows_plugin:
            raise AssertionError(f"Windows Plugin smoke lost product path {marker}")

    specialized = {
        path.name: pull_request_paths(path)
        for path in workflow_paths
        if path != BASELINE
    }
    for workflow_name, paths in specialized.items():
        for forbidden in ("cmd/**", "internal/**", "contracts/**", "tools/**"):
            if forbidden in paths:
                raise AssertionError(
                    f"{workflow_name} restores broad unrelated path {forbidden}"
                )
        if f".github/workflows/{workflow_name}" not in paths:
            raise AssertionError(f"{workflow_name} no longer self-validates")

    def triggered(change: str) -> set[str]:
        return {
            name
            for name, paths in specialized.items()
            if any(fnmatch.fnmatchcase(change, pattern) for pattern in paths)
        }

    assert triggered("tools/create_release_descriptor.py") == {"helper-build-spike.yml"}
    assert triggered("tools/build_helper.py") == {
        "guardian-lifecycle-spike.yml",
        "helper-build-spike.yml",
        "macos-signing-spike.yml",
        "windows-signing-spike.yml",
    }
    assert triggered("internal/guardian/manager.go") == {"guardian-lifecycle-spike.yml"}
    assert triggered("tools/test_macos_signing.py") == {"macos-signing-spike.yml"}
    assert triggered("tools/test_windows_signing.ps1") == {"windows-signing-spike.yml"}
    assert triggered("plugins/codex-skin/.codex-plugin/plugin.json") == {
        "windows-plugin-spike.yml"
    }
    assert triggered("go.mod") == {
        "guardian-lifecycle-spike.yml",
        "helper-build-spike.yml",
        "macos-signing-spike.yml",
        "windows-signing-spike.yml",
    }
    for unrelated in (
        "contracts/export-manifest.json",
        "fixtures/free-test-theme-v1/manifest.json",
        "tools/test_theme_fixture_validation.py",
        "tools/ci_scope.py",
        "tools/test_ci_workflows.py",
    ):
        if triggered(unrelated):
            raise AssertionError(
                f"unrelated {unrelated} starts specialized workflows: {triggered(unrelated)}"
            )

    template = (ROOT / ".github" / "pull_request_template.md").read_text()
    for marker in (
        "Repo scope: `plugin` / `both`",
        "Paired Private PR (`both` only; otherwise `N/A`)",
        "Private final 40-character commit SHA",
        "Public final 40-character commit SHA",
        "Exact handoff allowlist (`Private path -> Public path`",
        "Final head is frozen",
        "does not require a Private branch/twin",
    ):
        if marker not in template:
            raise AssertionError(f"Public PR template is missing scope marker: {marker}")

    print(
        "Public CI workflow tests passed (independent baseline; path-scoped platform matrices; no twin)."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
