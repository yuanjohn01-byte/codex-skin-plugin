#!/usr/bin/env python3
"""Unit tests for fail-closed Public component CI selection."""

from __future__ import annotations

import subprocess
from unittest.mock import patch

from ci_scope import (
    FULL_SELECTION,
    changed_paths,
    is_normal_main_merge,
    normalize_path,
    select_ci,
)


def main() -> int:
    assert normalize_path("./.gitattributes") == ".gitattributes"
    assert normalize_path(r".github\workflows\ci.yml") == ".github/workflows/ci.yml"

    for paths in (["README.md"], ["AGENTS.md", "SECURITY.md"]):
        selection = select_ci(paths, "pull_request")
        assert selection.ci_profile == "fast"
        assert not selection.run_fixture and not selection.run_go

    for fixture_path in (
        "fixtures/free-test-theme-v1/manifest.json",
        "contracts/export-manifest.json",
        "tools/test_theme_fixture_validation.py",
    ):
        selection = select_ci([fixture_path], "pull_request")
        assert selection.ci_profile == "standard"
        assert selection.run_fixture and not selection.run_go

    for go_path in (
        "cmd/codex-skin/main.go",
        "internal/bootstrap/bootstrap.go",
        "internal/guardian/manager.go",
        "contracts/helper-protocol-v1.schema.json",
        "tools/build_helper.py",
    ):
        selection = select_ci([go_path], "pull_request")
        assert selection.ci_profile == "standard"
        assert selection.run_go and not selection.run_fixture

    plugin = select_ci(["plugins/codex-skin/.codex-plugin/plugin.json"], "pull_request")
    assert plugin.ci_profile == "standard"
    assert not plugin.run_fixture and not plugin.run_go

    for full_path in (
        ".github/workflows/ci.yml",
        "tools/ci_scope.py",
        "go.mod",
        "go.sum",
        "unknown/new-release-input.toml",
    ):
        assert select_ci([full_path], "pull_request") == FULL_SELECTION
    assert select_ci(None, "pull_request") == FULL_SELECTION
    assert select_ci(["README.md"], "workflow_dispatch") == FULL_SELECTION
    assert select_ci(["README.md"], "release") == FULL_SELECTION
    assert select_ci(["README.md"], "schedule") == FULL_SELECTION

    merge_main = select_ci(None, "push", normal_main_merge=True)
    assert merge_main.ci_profile == "fast" and merge_main.lightweight_main
    assert not merge_main.run_fixture and not merge_main.run_go
    assert select_ci(["README.md"], "push") == FULL_SELECTION

    successful_diff = subprocess.CompletedProcess(
        args=[], returncode=0, stdout="README.md\n", stderr=""
    )
    failed_diff = subprocess.CompletedProcess(
        args=[], returncode=128, stdout="", stderr="unknown revision"
    )
    with patch("ci_scope.subprocess.run", return_value=successful_diff):
        assert changed_paths("a" * 40, "b" * 40) == ["README.md"]
    with patch("ci_scope.subprocess.run", return_value=failed_diff):
        assert changed_paths("a" * 40, "b" * 40) is None
    assert changed_paths("0" * 40, "b" * 40) is None

    merge_parents = subprocess.CompletedProcess(
        args=[], returncode=0, stdout=f"{'a' * 40} {'c' * 40}\n", stderr=""
    )
    direct_parent = subprocess.CompletedProcess(
        args=[], returncode=0, stdout=f"{'a' * 40}\n", stderr=""
    )
    with patch("ci_scope.subprocess.run", return_value=merge_parents):
        assert is_normal_main_merge(
            "push", "refs/heads/main", "a" * 40, "b" * 40
        )
        assert not is_normal_main_merge(
            "push", "refs/heads/dev", "a" * 40, "b" * 40
        )
    with patch("ci_scope.subprocess.run", return_value=direct_parent):
        assert not is_normal_main_merge(
            "push", "refs/heads/main", "a" * 40, "b" * 40
        )

    print("Public CI scope tests passed (fixture/Go; event/ref/first-parent; fail closed).")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
