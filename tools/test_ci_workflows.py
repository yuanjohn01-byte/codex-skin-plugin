#!/usr/bin/env python3
"""Prevent duplicate feature-push and pull-request workflow executions."""

from __future__ import annotations

from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
WORKFLOWS = ROOT / ".github" / "workflows"
BASELINE = WORKFLOWS / "ci.yml"


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

    template = (ROOT / ".github" / "pull_request_template.md").read_text()
    for marker in (
        "Repo scope: `plugin` / `both`",
        "Paired Private PR/ref (`both` only; otherwise `N/A`)",
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
