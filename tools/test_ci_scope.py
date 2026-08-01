#!/usr/bin/env python3
"""Unit tests for fail-closed Public component CI selection."""

from __future__ import annotations

import subprocess
import tempfile
from pathlib import Path
from unittest.mock import patch

from ci_scope import (
    COMPLETE_FULL_SELECTION,
    FULL_SELECTION,
    PAID_ALPHA_FULL_SELECTION,
    changed_paths,
    is_normal_main_merge,
    normalize_path,
    select_ci,
)


def git(root: Path, *args: str) -> str:
    result = subprocess.run(
        ["git", *args],
        cwd=root,
        check=False,
        capture_output=True,
        text=True,
        input="",
    )
    if result.returncode != 0:
        raise AssertionError(result.stdout + result.stderr)
    return result.stdout.strip()


def assert_real_pr_diff_graph(base_only_path: str) -> None:
    """Prove base-only advances never contaminate a PR path selection."""
    with tempfile.TemporaryDirectory(prefix="codex-skin-public-ci-graph-") as raw_root:
        source = Path(raw_root) / "source"
        source.mkdir()
        git(source, "init", "-b", "main")
        (source / "README.md").write_text("base\n", encoding="utf-8")
        git(source, "add", "README.md")
        git(
            source,
            "-c",
            "user.name=CI Scope Test",
            "-c",
            "user.email=ci-scope@example.invalid",
            "commit",
            "-m",
            "base",
        )
        git(source, "switch", "-c", "feature")
        (source / "README.md").write_text("feature\n", encoding="utf-8")
        git(source, "add", "README.md")
        git(
            source,
            "-c",
            "user.name=CI Scope Test",
            "-c",
            "user.email=ci-scope@example.invalid",
            "commit",
            "-m",
            "feature docs",
        )
        feature_head = git(source, "rev-parse", "HEAD")
        git(source, "switch", "main")
        (source / base_only_path).write_text("base advanced\n", encoding="utf-8")
        git(source, "add", base_only_path)
        git(
            source,
            "-c",
            "user.name=CI Scope Test",
            "-c",
            "user.email=ci-scope@example.invalid",
            "commit",
            "-m",
            "advance base",
        )
        base_head = git(source, "rev-parse", "HEAD")

        paths = changed_paths(base_head, feature_head, source)
        assert paths == ["README.md"], paths
        assert select_ci(paths, "pull_request").ci_profile == "fast"

        empty_tree = git(source, "mktree")
        orphan = git(
            source,
            "-c",
            "user.name=CI Scope Test",
            "-c",
            "user.email=ci-scope@example.invalid",
            "commit-tree",
            empty_tree,
            "-m",
            "unrelated root",
        )
        assert changed_paths(base_head, orphan, source) is None
        assert select_ci(None, "pull_request") == FULL_SELECTION

        shallow = Path(raw_root) / "shallow"
        git(
            Path(raw_root),
            "clone",
            "--depth",
            "1",
            "--branch",
            "feature",
            source.as_uri(),
            str(shallow),
        )
        assert changed_paths(base_head, feature_head, shallow) is None
        assert select_ci(None, "pull_request") == FULL_SELECTION
        assert changed_paths("f" * 40, feature_head, source) is None
        assert changed_paths("main", feature_head, source) is None


def main() -> int:
    assert normalize_path("./.gitattributes") == ".gitattributes"
    assert normalize_path(r".github\workflows\ci.yml") == ".github/workflows/ci.yml"

    for paths in (["README.md"], ["AGENTS.md", "SECURITY.md"]):
        selection = select_ci(paths, "pull_request")
        assert selection.ci_profile == "fast"
        assert not any(
            value is True
            for name, value in selection.outputs()
            if name.startswith("run_")
        )

    for fixture_path in (
        "fixtures/free-test-theme-v1/manifest.json",
        "contracts/export-manifest.json",
        "tools/test_theme_fixture_validation.py",
    ):
        selection = select_ci([fixture_path], "pull_request")
        assert selection.ci_profile == "standard"
        assert selection.run_fixture and not selection.run_go
        assert not selection.run_helper_build

    for go_path in (
        "cmd/codex-skin/main.go",
        "internal/bootstrap/bootstrap.go",
        "contracts/helper-protocol-v1.schema.json",
        "tools/build_helper.py",
    ):
        selection = select_ci([go_path], "pull_request")
        assert selection.ci_profile == "standard"
        assert selection.run_go and selection.run_helper_build
        assert not selection.run_fixture
        assert not selection.run_guardian_lifecycle
        assert not selection.run_macos_signing
        assert not selection.run_windows_signing

    plugin = select_ci(["plugins/codex-skin/.codex-plugin/plugin.json"], "pull_request")
    assert plugin.ci_profile == "standard"
    assert plugin.run_windows_plugin
    assert not plugin.run_fixture and not plugin.run_go

    guardian = select_ci(["internal/guardian/manager.go"], "pull_request")
    assert guardian.ci_profile == "standard"
    assert guardian.run_guardian_lifecycle
    assert not guardian.run_go and not guardian.run_helper_build
    assert not guardian.run_macos_signing and not guardian.run_windows_signing

    macos_signing = select_ci(["tools/test_macos_signing.py"], "pull_request")
    assert macos_signing.ci_profile == "standard"
    assert macos_signing.run_macos_signing
    assert not macos_signing.run_helper_build

    windows_signing = select_ci(
        ["tools/test_windows_signing.ps1"], "pull_request"
    )
    assert windows_signing.ci_profile == "standard"
    assert windows_signing.run_windows_signing
    assert not windows_signing.run_helper_build

    for full_path in (
        ".github/workflows/ci.yml",
        "tools/ci_scope.py",
        "go.mod",
        "go.sum",
        "internal/adapter/live.go",
        "internal/appearance/config.go",
        "internal/cdp/client.go",
        "internal/codex/identity_windows.go",
        "internal/engine/engine.go",
        "internal/theme/contract.go",
        "tools/gateb_calibration/main.go",
        "unknown/new-release-input.toml",
    ):
        assert select_ci([full_path], "pull_request") == PAID_ALPHA_FULL_SELECTION
    assert select_ci(None, "pull_request") == PAID_ALPHA_FULL_SELECTION
    assert (
        select_ci(
            ["README.md"],
            "workflow_dispatch",
            manual_profile="paid-alpha",
        )
        == PAID_ALPHA_FULL_SELECTION
    )
    assert (
        select_ci(["README.md"], "workflow_dispatch", manual_profile="full")
        == COMPLETE_FULL_SELECTION
    )
    assert select_ci(["README.md"], "release") == COMPLETE_FULL_SELECTION
    assert select_ci(["README.md"], "schedule") == PAID_ALPHA_FULL_SELECTION

    merge_main = select_ci(None, "push", normal_main_merge=True)
    assert merge_main.ci_profile == "fast" and merge_main.lightweight_main
    assert not merge_main.run_fixture and not merge_main.run_go
    assert select_ci(["README.md"], "push") == COMPLETE_FULL_SELECTION

    workflow_changes = select_ci(
        [
            ".github/workflows/ci.yml",
            ".github/workflows/guardian-lifecycle-spike.yml",
            ".github/workflows/macos-signing-spike.yml",
            ".github/workflows/windows-signing-spike.yml",
        ],
        "pull_request",
    )
    assert workflow_changes.ci_profile == "full"
    assert workflow_changes.run_fixture
    assert workflow_changes.run_go
    assert workflow_changes.run_helper_build
    assert workflow_changes.run_windows_plugin
    assert workflow_changes.run_guardian_lifecycle
    assert workflow_changes.run_macos_signing
    assert workflow_changes.run_windows_signing
    assert workflow_changes.run_full
    assert not workflow_changes.run_complete_full

    assert_real_pr_diff_graph("go.mod")
    assert changed_paths("0" * 40, "b" * 40) is None

    ambiguous_bases = subprocess.CompletedProcess(
        args=[], returncode=0, stdout=f"{'c' * 40}\n{'d' * 40}\n", stderr=""
    )
    with patch("ci_scope.subprocess.run", return_value=ambiguous_bases):
        assert changed_paths("a" * 40, "b" * 40) is None

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
