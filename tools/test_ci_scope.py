#!/usr/bin/env python3
"""Unit tests for fail-closed Public component CI selection."""

from __future__ import annotations

import subprocess
import tempfile
from pathlib import Path
from unittest.mock import patch

from ci_scope import (
    FULL_SELECTION,
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
