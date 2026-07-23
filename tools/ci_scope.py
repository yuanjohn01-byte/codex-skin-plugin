#!/usr/bin/env python3
"""Select fail-closed, component-aware GitHub Actions checks for Public."""

from __future__ import annotations

import argparse
import re
import subprocess
from dataclasses import dataclass
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
IMMUTABLE_GIT_SHA = re.compile(r"^[0-9a-f]{40}$")
MACHINE_RULE_PATHS = {"tools/ci_scope.py", "tools/test_ci_scope.py", "tools/test_ci_workflows.py"}
FAST_GOVERNANCE_PATHS = {
    ".gitignore",
    ".github/CODEOWNERS",
    ".github/pull_request_template.md",
}
PLUGIN_BOUNDARY_PATHS = {
    ".gitattributes",
    ".agents/plugins/marketplace.json",
}
FIXTURE_PATHS = {
    "contracts/export-manifest.json",
    "tools/test_theme_fixture.py",
    "tools/test_theme_fixture_validation.py",
    "tools/test_public_repository.py",
    "tools/validate_public_repo.py",
}
GO_TOOL_PATHS = {
    "tools/build_guardian.py",
    "tools/build_helper.py",
    "tools/create_release_descriptor.py",
    "tools/test_guardian_builds.py",
    "tools/test_guardian_macos.py",
    "tools/test_guardian_windows.ps1",
    "tools/test_helper_builds.py",
    "tools/test_macos_signing.py",
    "tools/test_release_descriptor.py",
    "tools/test_windows_signing.ps1",
}


@dataclass(frozen=True)
class CISelection:
    ci_profile: str
    run_fixture: bool = False
    run_go: bool = False
    run_full: bool = False
    lightweight_main: bool = False

    def outputs(self) -> tuple[tuple[str, bool | str], ...]:
        return (
            ("ci_profile", self.ci_profile),
            ("run_fixture", self.run_fixture),
            ("run_go", self.run_go),
            ("run_full", self.run_full),
            ("lightweight_main", self.lightweight_main),
        )


FULL_SELECTION = CISelection(
    ci_profile="full", run_fixture=True, run_go=True, run_full=True
)


def normalize_path(path: str) -> str:
    normalized = path.replace("\\", "/")
    while normalized.startswith("./"):
        normalized = normalized[2:]
    return normalized


def _is_durable_text(path: str) -> bool:
    return (
        path.startswith("docs/")
        or path == "AGENTS.md"
        or path in {"LICENSE", "SECURITY.md"}
        or ("/" not in path and path.lower().endswith((".md", ".mdx")))
    )


def _component_for_path(path: str) -> set[str] | None:
    if path.startswith(".github/workflows/") or path in MACHINE_RULE_PATHS:
        return {"full"}
    if path in {"go.mod", "go.sum"}:
        return {"full"}
    if _is_durable_text(path) or path in FAST_GOVERNANCE_PATHS:
        return set()
    if path in PLUGIN_BOUNDARY_PATHS or path.startswith("plugins/"):
        return {"boundary"}
    if path.startswith("fixtures/") or path in FIXTURE_PATHS:
        return {"fixture"}
    if path.startswith("contracts/"):
        return {"go"}
    if path.startswith(("cmd/", "internal/")) or path in GO_TOOL_PATHS:
        return {"go"}
    return None


def select_ci(
    paths: list[str] | None,
    event_name: str,
    *,
    normal_main_merge: bool = False,
) -> CISelection:
    if event_name in {"workflow_dispatch", "release"}:
        return FULL_SELECTION
    if event_name == "push":
        if normal_main_merge:
            return CISelection(ci_profile="fast", lightweight_main=True)
        return FULL_SELECTION
    if event_name != "pull_request" or not paths:
        return FULL_SELECTION

    groups: set[str] = set()
    for raw_path in paths:
        selected = _component_for_path(normalize_path(raw_path))
        if selected is None or "full" in selected:
            return FULL_SELECTION
        groups.update(selected)
    if not groups:
        return CISelection(ci_profile="fast")
    return CISelection(
        ci_profile="standard",
        run_fixture="fixture" in groups,
        run_go="go" in groups,
    )


def changed_paths(base_sha: str, head_sha: str, root: Path = ROOT) -> list[str] | None:
    if (
        IMMUTABLE_GIT_SHA.fullmatch(base_sha) is None
        or IMMUTABLE_GIT_SHA.fullmatch(head_sha) is None
        or set(base_sha) == {"0"}
    ):
        return None
    merge_base_result = subprocess.run(
        ["git", "merge-base", "--all", base_sha, head_sha],
        cwd=root,
        check=False,
        capture_output=True,
        text=True,
    )
    merge_bases = [line for line in merge_base_result.stdout.splitlines() if line]
    if (
        merge_base_result.returncode != 0
        or len(merge_bases) != 1
        or IMMUTABLE_GIT_SHA.fullmatch(merge_bases[0]) is None
    ):
        return None
    result = subprocess.run(
        [
            "git",
            "diff",
            "--name-only",
            "--no-renames",
            merge_bases[0],
            head_sha,
            "--",
        ],
        cwd=root,
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        return None
    paths = [line for line in result.stdout.splitlines() if line]
    return paths or None


def is_normal_main_merge(
    event_name: str,
    ref: str,
    base_sha: str,
    head_sha: str,
    root: Path = ROOT,
) -> bool:
    if (
        event_name != "push"
        or ref != "refs/heads/main"
        or IMMUTABLE_GIT_SHA.fullmatch(base_sha) is None
        or IMMUTABLE_GIT_SHA.fullmatch(head_sha) is None
        or set(base_sha) == {"0"}
    ):
        return False
    result = subprocess.run(
        ["git", "show", "-s", "--format=%P", head_sha],
        cwd=root,
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        return False
    parents = result.stdout.strip().split()
    return len(parents) == 2 and parents[0] == base_sha


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--event", required=True)
    parser.add_argument("--ref", default="")
    parser.add_argument("--base", default="")
    parser.add_argument("--head", required=True)
    args = parser.parse_args()

    normal_merge = is_normal_main_merge(
        args.event, args.ref, args.base, args.head
    )
    paths = None if args.event != "pull_request" else changed_paths(args.base, args.head)
    selection = select_ci(paths, args.event, normal_main_merge=normal_merge)
    for name, value in selection.outputs():
        rendered = str(value).lower() if isinstance(value, bool) else value
        print(f"{name}={rendered}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
