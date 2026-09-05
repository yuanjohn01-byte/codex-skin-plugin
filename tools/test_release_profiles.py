#!/usr/bin/env python3
"""Fail-closed tests for immutable Staging and Production release profiles."""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
from pathlib import Path

from build_bootstrap import bootstrap_targets
from build_helper import helper_targets
from create_release_descriptor import descriptor_from_summary
from release_profiles import PRODUCTION, STAGING, WINDOWS_TEST, ReleaseProfile


ROOT = Path(__file__).resolve().parents[1]
PLATFORMS = ("macos-arm64", "macos-x64", "windows-x64")
HELPER_SUFFIXES = ("macos_arm64", "macos_x64", "windows_x64.exe")
BOOTSTRAP_SUFFIXES = ("macos_arm64", "macos_x64", "windows_x64.exe")


def helper_summary(profile: ReleaseProfile) -> dict[str, object]:
    return {
        "schemaVersion": 1,
        "releaseProfile": profile.name,
        "apiBaseURL": profile.api_base_url,
        "artifacts": [
            {
                "platform": platform,
                "filename": f"codex-skin-helper_{profile.helper_version}_{suffix}",
                "helperVersion": profile.helper_version,
                "helperReleaseTag": profile.helper_release_tag,
                "builtAt": "2026-08-24T08:00:00Z",
                "buildCommit": "a" * 40,
                "sha256": digest * 64,
                "size": 1_900_000 + index,
            }
            for index, (platform, suffix, digest) in enumerate(
                zip(PLATFORMS, HELPER_SUFFIXES, ("a", "b", "c"), strict=True)
            )
        ],
    }


def bootstrap_summary(profile: ReleaseProfile) -> dict[str, object]:
    return {
        "schemaVersion": 1,
        "releaseProfile": profile.name,
        "helperAPIBaseURL": profile.api_base_url,
        "artifacts": [
            {
                "platform": platform,
                "filename": f"codex-skin-bootstrap_{profile.bootstrap_version}_{suffix}",
                "bootstrapVersion": profile.bootstrap_version,
                "helperReleaseTag": profile.helper_release_tag,
                "builtAt": "2026-08-24T08:00:00Z",
                "buildCommit": "a" * 40,
                "sha256": digest * 64,
                "size": 900_000 + index,
            }
            for index, (platform, suffix, digest) in enumerate(
                zip(PLATFORMS, BOOTSTRAP_SUFFIXES, ("d", "e", "f"), strict=True)
            )
        ],
    }


def run(*arguments: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, *arguments],
        cwd=ROOT,
        check=False,
        capture_output=True,
        text=True,
    )


def main() -> int:
    if (
        STAGING.helper_version != "0.1.0-paid-alpha.16"
        or STAGING.bootstrap_version != "0.1.0-paid-alpha.15"
        or PRODUCTION.helper_version != "0.1.0-paid-alpha.17"
        or PRODUCTION.bootstrap_version != "0.1.0-paid-alpha.16"
        or STAGING.signing_key_id != "helper-alpha-2026-08"
        or PRODUCTION.signing_key_id != "helper-production-2026-08"
        or PRODUCTION.api_base_url != "https://codexskin.ai"
        or STAGING.api_base_url == PRODUCTION.api_base_url
    ):
        raise AssertionError("fixed release profile versions or origins drifted")

    for profile in (STAGING, PRODUCTION, WINDOWS_TEST):
        helper_names = [target.filename for target in helper_targets(profile.helper_version)]
        bootstrap_names = [target.filename for target in bootstrap_targets(profile.bootstrap_version)]
        if any(profile.helper_version not in name for name in helper_names):
            raise AssertionError(f"{profile.name} Helper filename lost its immutable version")
        if any(profile.bootstrap_version not in name for name in bootstrap_names):
            raise AssertionError(f"{profile.name} Bootstrap filename lost its immutable version")
        descriptor = descriptor_from_summary(
            helper_summary(profile),
            profile.signing_key_id,
            profile.name,
        )
        if (
            descriptor["helperVersion"] != profile.helper_version
            or descriptor["releaseTag"] != profile.helper_release_tag
        ):
            raise AssertionError(f"{profile.name} descriptor is not channel-specific")

    try:
        descriptor_from_summary(
            helper_summary(STAGING),
            STAGING.signing_key_id,
            PRODUCTION.name,
        )
    except ValueError:
        pass
    else:
        raise AssertionError("Production descriptor accepted a Staging build summary")

    try:
        descriptor_from_summary(
            helper_summary(PRODUCTION),
            PRODUCTION.signing_key_id,
        )
    except ValueError:
        pass
    else:
        raise AssertionError("Production descriptor accepted a missing release-profile assertion")

    for profile, foreign_key_id in (
        (STAGING, PRODUCTION.signing_key_id),
        (PRODUCTION, STAGING.signing_key_id),
        (WINDOWS_TEST, STAGING.signing_key_id),
    ):
        try:
            descriptor_from_summary(
                helper_summary(profile),
                foreign_key_id,
                profile.name,
            )
        except ValueError as exc:
            if "signing key does not match" not in str(exc):
                raise
        else:
            raise AssertionError(f"{profile.name} descriptor accepted the other channel's signing key")

    with tempfile.TemporaryDirectory(prefix="codex-skin-release-profiles-") as directory:
        root = Path(directory)
        for profile in (STAGING, PRODUCTION, WINDOWS_TEST):
            helper_path = root / f"helper-{profile.name}.json"
            helper_path.write_text(json.dumps(helper_summary(profile)), encoding="utf-8")
            sbom_path = root / f"sbom-{profile.name}.json"
            sbom = run(
                "tools/create_sbom.py",
                "--summary",
                str(helper_path),
                "--output",
                str(sbom_path),
                "--release-profile",
                profile.name,
            )
            if sbom.returncode != 0:
                raise AssertionError(sbom.stdout + sbom.stderr)
            sbom_payload = json.loads(sbom_path.read_text(encoding="utf-8"))
            if (
                sbom_payload.get("name")
                != f"codex-skin-helper-{profile.helper_version}-{profile.name}-sbom"
                or f"/sbom/{profile.name}/" not in sbom_payload.get("documentNamespace", "")
            ):
                raise AssertionError(f"{profile.name} SBOM lost its release-profile identity")

            bootstrap_path = root / f"bootstrap-{profile.name}.json"
            bootstrap_path.write_text(json.dumps(bootstrap_summary(profile)), encoding="utf-8")
            shell_path = root / f"bootstrap-pins-{profile.name}.sh"
            powershell_path = root / f"bootstrap-pins-{profile.name}.ps1"
            pins = run(
                "tools/render_bootstrap_pins.py",
                "--summary",
                str(bootstrap_path),
                "--release-profile",
                profile.name,
                "--shell-output",
                str(shell_path),
                "--powershell-output",
                str(powershell_path),
            )
            if pins.returncode != 0:
                raise AssertionError(pins.stdout + pins.stderr)
            shell = shell_path.read_text(encoding="utf-8")
            powershell = powershell_path.read_text(encoding="utf-8")
            for marker in (
                profile.name,
                profile.api_host,
                profile.helper_release_tag,
                profile.bootstrap_version,
            ):
                if marker not in shell or marker not in powershell:
                    raise AssertionError(f"{profile.name} generated pins are ambiguous: {marker}")

        mixed = run(
            "tools/render_bootstrap_pins.py",
            "--summary",
            str(root / "bootstrap-staging.json"),
            "--release-profile",
            PRODUCTION.name,
            "--shell-output",
            str(root / "mixed.sh"),
            "--powershell-output",
            str(root / "mixed.ps1"),
        )
        if mixed.returncode == 0:
            raise AssertionError("Production pins accepted a Staging Bootstrap summary")

        for index, protected_origin in enumerate(
            (
                PRODUCTION.api_base_url,
                "https://codexskin.ai:443",
                "https://CODEXSKIN.AI",
                "https://codexskin.ai.",
                "https://CODEX-SKIN-STAGING.YUANJOHN01.WORKERS.DEV.",
            )
        ):
            unprofiled = run(
                "tools/build_helper.py",
                "--api-base-url",
                protected_origin,
                "--target",
                "macos-arm64",
                "--output",
                str(root / f"unprofiled-{index}"),
            )
            if (
                unprofiled.returncode == 0
                or "require a fixed release profile" not in unprofiled.stderr
            ):
                raise AssertionError(
                    f"unprofiled protected-origin Helper build did not fail closed: {protected_origin}"
                )

    print(
        "Release profile tests passed (fixed versions/origins/keys; descriptor, SBOM, pins isolation; mixed inputs rejected)."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
