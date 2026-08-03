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
HELPER_TOOL_PATHS = {
    "tools/attest_installation.py",
    "tools/build_bootstrap.py",
    "tools/build_helper.py",
    "tools/create_release_descriptor.py",
    "tools/render_bootstrap_pins.py",
    "tools/test_bootstrap_builds.py",
    "tools/test_helper_builds.py",
    "tools/test_installation_attestation.py",
    "tools/test_plugin_bootstrap_entry.py",
    "tools/test_release_descriptor.py",
}
GUARDIAN_TOOL_PATHS = {
    "tools/build_guardian.py",
    "tools/test_guardian_builds.py",
    "tools/test_guardian_macos.py",
    "tools/test_guardian_windows.ps1",
}
SPECIALIZED_WORKFLOW_GROUPS = {
    ".github/workflows/helper-build-spike.yml": {"go", "helper_build"},
    ".github/workflows/helper-release-candidate.yml": {"paid_alpha_full"},
    ".github/workflows/guardian-lifecycle-spike.yml": {"guardian_lifecycle"},
    ".github/workflows/macos-signing-spike.yml": {"macos_signing"},
    ".github/workflows/windows-signing-spike.yml": {"windows_signing"},
    ".github/workflows/windows-plugin-spike.yml": {"windows_plugin"},
}


@dataclass(frozen=True)
class CISelection:
    ci_profile: str
    run_fixture: bool = False
    run_go: bool = False
    run_helper_build: bool = False
    run_guardian_lifecycle: bool = False
    run_macos_signing: bool = False
    run_windows_signing: bool = False
    run_windows_plugin: bool = False
    run_full: bool = False
    run_complete_full: bool = False
    lightweight_main: bool = False

    def outputs(self) -> tuple[tuple[str, bool | str], ...]:
        return (
            ("ci_profile", self.ci_profile),
            ("run_fixture", self.run_fixture),
            ("run_go", self.run_go),
            ("run_helper_build", self.run_helper_build),
            ("run_guardian_lifecycle", self.run_guardian_lifecycle),
            ("run_macos_signing", self.run_macos_signing),
            ("run_windows_signing", self.run_windows_signing),
            ("run_windows_plugin", self.run_windows_plugin),
            ("run_full", self.run_full),
            ("run_complete_full", self.run_complete_full),
            ("lightweight_main", self.lightweight_main),
        )


PAID_ALPHA_FULL_SELECTION = CISelection(
    ci_profile="full",
    run_fixture=True,
    run_go=True,
    run_helper_build=True,
    run_windows_plugin=True,
    run_full=True,
)
COMPLETE_FULL_SELECTION = CISelection(
    ci_profile="full",
    run_fixture=True,
    run_go=True,
    run_helper_build=True,
    run_guardian_lifecycle=True,
    run_macos_signing=True,
    run_windows_signing=True,
    run_windows_plugin=True,
    run_full=True,
    run_complete_full=True,
)
FULL_SELECTION = PAID_ALPHA_FULL_SELECTION


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
    if path in MACHINE_RULE_PATHS or path == ".github/workflows/ci.yml":
        return {"paid_alpha_full"}
    if path in SPECIALIZED_WORKFLOW_GROUPS:
        return SPECIALIZED_WORKFLOW_GROUPS[path]
    if path.startswith(".github/workflows/"):
        return {"paid_alpha_full"}
    if path in {"go.mod", "go.sum"}:
        return {"paid_alpha_full"}
    if _is_durable_text(path) or path in FAST_GOVERNANCE_PATHS:
        return set()
    if path in PLUGIN_BOUNDARY_PATHS or path.startswith("plugins/"):
        return {"windows_plugin"}
    if path.startswith("fixtures/") or path in FIXTURE_PATHS:
        return {"fixture"}
    if path.startswith("contracts/"):
        return {"go", "helper_build"}
    if path.startswith(("cmd/codex-skin-guardian/", "internal/guardian/")):
        return {"guardian_lifecycle"}
    if path.startswith("internal/guardiancli/") or path in GUARDIAN_TOOL_PATHS:
        return {"guardian_lifecycle"}
    if path.startswith(
        (
            "internal/adapter/",
            "internal/appearance/",
            "internal/cdp/",
            "internal/codex/",
            "internal/engine/",
            "internal/theme/",
            "tools/gateb_calibration/",
        )
    ):
        return {"paid_alpha_full"}
    if (
        path.startswith(
            (
                "cmd/codex-skin/",
                "internal/bootstrap/",
                "internal/buildinfo/",
                "internal/cli/",
                "internal/credentials/",
                "internal/deviceauth/",
                "internal/protocol/",
                "internal/release/",
            )
        )
        or path in HELPER_TOOL_PATHS
    ):
        return {"go", "helper_build"}
    if path == "tools/test_macos_signing.py":
        return {"macos_signing"}
    if path == "tools/test_windows_signing.ps1":
        return {"windows_signing"}
    if path.startswith(("cmd/", "internal/")):
        return {"paid_alpha_full"}
    return None


def select_ci(
    paths: list[str] | None,
    event_name: str,
    *,
    normal_main_merge: bool = False,
    manual_profile: str = "full",
) -> CISelection:
    if event_name == "release":
        return COMPLETE_FULL_SELECTION
    if event_name == "workflow_dispatch":
        if manual_profile == "paid-alpha":
            return PAID_ALPHA_FULL_SELECTION
        return COMPLETE_FULL_SELECTION
    if event_name == "push":
        if normal_main_merge:
            return CISelection(ci_profile="fast", lightweight_main=True)
        return COMPLETE_FULL_SELECTION
    if event_name != "pull_request" or not paths:
        return PAID_ALPHA_FULL_SELECTION

    groups: set[str] = set()
    for raw_path in paths:
        selected = _component_for_path(normalize_path(raw_path))
        groups.update({"paid_alpha_full"} if selected is None else selected)
    if not groups:
        return CISelection(ci_profile="fast")
    paid_alpha_full = "paid_alpha_full" in groups
    return CISelection(
        ci_profile="full" if paid_alpha_full else "standard",
        run_fixture=paid_alpha_full or "fixture" in groups,
        run_go=paid_alpha_full or "go" in groups,
        run_helper_build=paid_alpha_full or "helper_build" in groups,
        run_guardian_lifecycle="guardian_lifecycle" in groups,
        run_macos_signing="macos_signing" in groups,
        run_windows_signing="windows_signing" in groups,
        run_windows_plugin=paid_alpha_full or "windows_plugin" in groups,
        run_full=paid_alpha_full,
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
    parser.add_argument(
        "--manual-profile",
        choices=("paid-alpha", "full"),
        default="full",
    )
    args = parser.parse_args()

    normal_merge = is_normal_main_merge(
        args.event, args.ref, args.base, args.head
    )
    paths = None if args.event != "pull_request" else changed_paths(args.base, args.head)
    selection = select_ci(
        paths,
        args.event,
        normal_main_merge=normal_merge,
        manual_profile=args.manual_profile,
    )
    for name, value in selection.outputs():
        rendered = str(value).lower() if isinstance(value, bool) else value
        print(f"{name}={rendered}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
