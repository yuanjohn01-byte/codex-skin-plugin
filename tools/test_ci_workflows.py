#!/usr/bin/env python3
"""Prove Public CI has one component-aware automatic PR router."""

from __future__ import annotations

from pathlib import Path

from ci_scope import select_ci


ROOT = Path(__file__).resolve().parents[1]
WORKFLOWS = ROOT / ".github" / "workflows"
BASELINE = WORKFLOWS / "ci.yml"


def job_block(workflow: str, job_name: str) -> str:
    marker = f"  {job_name}:\n"
    start = workflow.find(marker)
    if start < 0:
        raise AssertionError(f"missing workflow job: {job_name}")
    end = len(workflow)
    for candidate in range(start + len(marker), len(workflow)):
        if (
            workflow[candidate : candidate + 2] == "  "
            and workflow[candidate : candidate + 4] != "    "
            and (candidate == 0 or workflow[candidate - 1] == "\n")
        ):
            end = candidate
            break
    return workflow[start:end]


def selected_calls(
    paths: list[str] | None,
    event_name: str,
    *,
    normal_main_merge: bool = False,
    manual_profile: str = "full",
) -> set[str]:
    selection = select_ci(
        paths,
        event_name,
        normal_main_merge=normal_main_merge,
        manual_profile=manual_profile,
    )
    calls = {
        "helper-build-spike.yml": selection.run_helper_build,
        "guardian-lifecycle-spike.yml": selection.run_guardian_lifecycle,
        "macos-signing-spike.yml": selection.run_macos_signing,
        "windows-signing-spike.yml": selection.run_windows_signing,
        "windows-plugin-spike.yml": selection.run_windows_plugin,
    }
    return {name for name, enabled in calls.items() if enabled}


def main() -> int:
    workflow_paths = sorted(WORKFLOWS.glob("*.yml"))
    if not workflow_paths:
        raise AssertionError("no Public workflows found")

    for path in workflow_paths:
        workflow = path.read_text()
        if "codex/**" in workflow:
            raise AssertionError(f"{path.name} restores duplicate feature push CI")
        if "workflow_dispatch:" not in workflow or "concurrency:" not in workflow:
            raise AssertionError(f"{path.name} lost its manual/concurrency contract")
        if "cancel-in-progress: true" not in workflow:
            raise AssertionError(f"{path.name} does not cancel stale runs")
        for forbidden in (
            "yuanjohn01-byte/codex-skin.git",
            "repository: yuanjohn01-byte/codex-skin",
            "gh api repos/yuanjohn01-byte/codex-skin",
        ):
            if forbidden in workflow:
                raise AssertionError(f"{path.name} makes Public CI depend on Private")

    baseline = BASELINE.read_text()
    for marker in (
        "  pull_request:",
        "  push:",
        "      - main",
        "  release:",
        "      - published",
        "type: choice",
        "default: paid-alpha",
        "          - paid-alpha",
        "          - full",
        "repository-boundary:",
        "fetch-depth: 0",
        "tools/ci_scope.py",
        "--manual-profile",
        "run_fixture",
        "run_go",
        "run_helper_build",
        "run_guardian_lifecycle",
        "run_macos_signing",
        "run_windows_signing",
        "run_windows_plugin",
        "run_complete_full",
        "lightweight_main",
        "if: steps.scope.outputs.run_go == 'true'",
        "./internal/restartflow",
        "./internal/themeapi",
        "./internal/userflow",
    ):
        if marker not in baseline:
            raise AssertionError(f"Public baseline lost routing marker: {marker}")
    if "paths:" in baseline or "paths-ignore:" in baseline:
        raise AssertionError("Public baseline must remain the independent always-on PR gate")
    if baseline.count("actions/setup-go@v5") != 1:
        raise AssertionError("Public baseline must set up Go at most once")
    for broad_go_command in (
        'gofmt -l cmd internal',
        "go vet ./...",
        "go test ./...",
    ):
        if broad_go_command in baseline:
            raise AssertionError(
                f"Helper baseline still makes deferred Guardian blocking: {broad_go_command}"
            )

    routed_calls = {
        "full-helper-build": ("run_helper_build", "helper-build-spike.yml"),
        "full-guardian-lifecycle": (
            "run_guardian_lifecycle",
            "guardian-lifecycle-spike.yml",
        ),
        "full-macos-signing": ("run_macos_signing", "macos-signing-spike.yml"),
        "full-windows-signing": (
            "run_windows_signing",
            "windows-signing-spike.yml",
        ),
        "full-windows-plugin": ("run_windows_plugin", "windows-plugin-spike.yml"),
    }
    for job_name, (output_name, called_workflow) in routed_calls.items():
        block = job_block(baseline, job_name)
        for marker in (
            "needs: repository-boundary",
            f"needs.repository-boundary.outputs.{output_name} == 'true'",
            f"uses: ./.github/workflows/{called_workflow}",
            "contents: read",
        ):
            if marker not in block:
                raise AssertionError(f"{job_name} lost selector marker: {marker}")
        for forbidden in (
            "github.event_name == 'push'",
            "github.event_name == 'workflow_dispatch'",
            "runs-on:",
            "steps:",
        ):
            if forbidden in block:
                raise AssertionError(
                    f"{job_name} bypasses the selector with duplicate event logic"
                )

    for path in workflow_paths:
        if path == BASELINE:
            continue
        workflow = path.read_text()
        if "  pull_request:" in workflow or "  push:" in workflow or "  release:" in workflow:
            raise AssertionError(
                f"{path.name} bypasses the central automatic selector"
            )
        if "  workflow_call:" not in workflow:
            raise AssertionError(f"{path.name} cannot be called by central CI")
        expected_group = f"group: {path.stem}-${{{{ github.event_name }}}}-"
        if expected_group not in workflow or "${{ github.workflow }}" in workflow:
            raise AssertionError(f"{path.name} reusable concurrency is not component-scoped")

    helper_workflow = (WORKFLOWS / "helper-build-spike.yml").read_text()
    for marker in (
        "windows-no-node-smoke:",
        "macos-no-node-smoke:",
        "signed-production-only",
        "signed-production-candidate:",
        "environment: paid-alpha-production-release",
        "PRODUCTION_CONFIRMATION: ${{ inputs.production_confirmation }}",
        "RELEASE https://codexskin.ai HELPER .17 BOOTSTRAP .16",
        "--release-profile production",
        "0.1.0-paid-alpha.17",
        "0.1.0-paid-alpha.16-${{ steps.inputs.outputs.candidate_sha }}-production",
        "Test native Keychain storage and token rotation client",
        "Execute without Node or Go on PATH",
        "./internal/restartflow",
        "./internal/themeapi",
        "./internal/userflow",
    ):
        if marker not in helper_workflow:
            raise AssertionError(f"Helper workflow lost Paid Alpha platform check: {marker}")
    staging_job = job_block(helper_workflow, "signed-staging-candidate")
    production_job = job_block(helper_workflow, "signed-production-candidate")
    if "inputs.run_profile == 'signed-staging-only'" not in staging_job:
        raise AssertionError("signed Staging candidate is not isolated to its exact manual profile")
    if "inputs.run_profile == 'signed-production-only'" not in production_job:
        raise AssertionError("signed Production candidate is not isolated to its exact manual profile")
    if "codex-skin-staging.yuanjohn01.workers.dev" in production_job:
        raise AssertionError("Production candidate workflow contains the Staging API origin")
    if "https://codexskin.ai" not in production_job:
        raise AssertionError("Production candidate workflow lost the fixed Production API origin")
    if "--api-base-url" in production_job:
        raise AssertionError("Production candidate workflow allows an API origin override")
    for job_name, block, required_key, forbidden_key in (
        (
            "signed-staging-candidate",
            staging_job,
            "--key-id helper-alpha-2026-08",
            "--key-id helper-production-2026-08",
        ),
        (
            "signed-production-candidate",
            production_job,
            "--key-id helper-production-2026-08",
            "--key-id helper-alpha-2026-08",
        ),
    ):
        if required_key not in block:
            raise AssertionError(f"{job_name} lost its channel-specific signing key")
        if forbidden_key in block:
            raise AssertionError(f"{job_name} contains the other channel's signing key")
    for job_name, required, forbidden in (
        (
            "windows-no-node-smoke",
            (
                "codex-skin-helper_0.1.0-paid-alpha.17_windows_x64.exe",
                "codex-skin-bootstrap_0.1.0-paid-alpha.16_windows_x64.exe",
                "helper-v0.1.0-paid-alpha.17",
                "https://codexskin.ai",
            ),
            (
                "codex-skin-helper_0.1.0-paid-alpha.16_windows_x64.exe",
                "codex-skin-bootstrap_0.1.0-paid-alpha.15_windows_x64.exe",
            ),
        ),
        (
            "macos-no-node-smoke",
            (
                "codex-skin-helper_0.1.0-paid-alpha.17_$suffix",
                "codex-skin-bootstrap_0.1.0-paid-alpha.16_$suffix",
                "helper-v0.1.0-paid-alpha.17",
                "https://codexskin.ai",
            ),
            (
                "codex-skin-helper_0.1.0-paid-alpha.16_$suffix",
                "codex-skin-bootstrap_0.1.0-paid-alpha.15_$suffix",
            ),
        ),
    ):
        block = job_block(helper_workflow, job_name)
        for marker in required:
            if marker not in block:
                raise AssertionError(f"{job_name} lost Production RC marker: {marker}")
        for marker in forbidden:
            if marker in block:
                raise AssertionError(f"{job_name} still executes a Staging binary: {marker}")
    if "go test ./..." in helper_workflow:
        raise AssertionError("Helper workflow still runs deferred Guardian packages")

    assert selected_calls(["README.md"], "pull_request") == set()
    assert selected_calls(
        ["fixtures/free-test-theme-v1/manifest.json"], "pull_request"
    ) == set()
    assert selected_calls(["tools/build_helper.py"], "pull_request") == {
        "helper-build-spike.yml"
    }
    assert selected_calls(["internal/release/release.go"], "pull_request") == {
        "helper-build-spike.yml"
    }
    assert selected_calls(["internal/guardian/manager.go"], "pull_request") == {
        "guardian-lifecycle-spike.yml"
    }
    assert selected_calls(["tools/test_macos_signing.py"], "pull_request") == {
        "macos-signing-spike.yml"
    }
    assert selected_calls(["tools/test_windows_signing.ps1"], "pull_request") == {
        "windows-signing-spike.yml"
    }
    assert selected_calls(
        ["plugins/codex-skin/.codex-plugin/plugin.json"], "pull_request"
    ) == {"windows-plugin-spike.yml"}

    paid_alpha_calls = {"helper-build-spike.yml", "windows-plugin-spike.yml"}
    complete_calls = {
        "helper-build-spike.yml",
        "guardian-lifecycle-spike.yml",
        "macos-signing-spike.yml",
        "windows-signing-spike.yml",
        "windows-plugin-spike.yml",
    }
    assert selected_calls(["go.mod"], "pull_request") == paid_alpha_calls
    assert selected_calls(
        ["README.md"],
        "workflow_dispatch",
        manual_profile="paid-alpha",
    ) == paid_alpha_calls
    assert selected_calls(
        ["README.md"],
        "workflow_dispatch",
        manual_profile="full",
    ) == complete_calls
    assert selected_calls(["README.md"], "release") == complete_calls
    assert selected_calls(["README.md"], "push") == complete_calls
    assert (
        selected_calls(
            None,
            "push",
            normal_main_merge=True,
        )
        == set()
    )

    workflow_change_calls = selected_calls(
        [
            ".github/workflows/ci.yml",
            ".github/workflows/guardian-lifecycle-spike.yml",
            ".github/workflows/macos-signing-spike.yml",
            ".github/workflows/windows-signing-spike.yml",
        ],
        "pull_request",
    )
    assert workflow_change_calls == complete_calls

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
        "Public CI workflow tests passed (single PR router; Paid Alpha/full profiles; no twin)."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
