#!/usr/bin/env python3
"""Prove installed-release attestation accepts one exact closure and rejects drift."""

from __future__ import annotations

import hashlib
import json
import shutil
import subprocess
import tempfile
from pathlib import Path

import attest_installation


ROOT = Path(__file__).resolve().parents[1]
CANDIDATE = "a" * 40
BOOTSTRAP_COMMIT = "b" * 40
HELPER_COMMIT = CANDIDATE
MISMATCHED_HELPER_COMMIT = "c" * 40
API_ORIGIN = "https://codex-skin-staging.example.invalid"


def executable(path: Path, content: str) -> None:
    path.write_text(content, encoding="utf-8")
    path.chmod(0o700)


def main() -> int:
    with tempfile.TemporaryDirectory(prefix="codex-skin-attestation-") as temporary:
        temporary_root = Path(temporary)
        plugin = temporary_root / "plugin"
        shutil.copytree(ROOT / "plugins" / "codex-skin", plugin)
        launcher = plugin / "scripts" / ".bootstrap" / "codex-skin-bootstrap_0.1.0-paid-alpha.10_macos_arm64"
        launcher.parent.mkdir(mode=0o700)
        executable(launcher, "#!/bin/sh\nexit 0\n")
        launcher_sha = hashlib.sha256(launcher.read_bytes()).hexdigest()
        (plugin / "scripts" / "bootstrap-pins.sh").write_text(
            "# Generated fixture pins.\n"
            "bootstrap_release_tag='helper-v0.1.0-paid-alpha.11'\n"
            "bootstrap_version='0.1.0-paid-alpha.10'\n"
            f"bootstrap_build_commit='{BOOTSTRAP_COMMIT}'\n"
            "bootstrap_built_at='2026-08-03T00:00:00Z'\n"
            "case \"$(uname -m)\" in\n"
            "  arm64)\n"
            "    bootstrap_filename='codex-skin-bootstrap_0.1.0-paid-alpha.10_macos_arm64'\n"
            f"    bootstrap_sha256='{launcher_sha}'\n"
            "    ;;\n"
            "  x86_64)\n"
            "    bootstrap_filename='codex-skin-bootstrap_0.1.0-paid-alpha.10_macos_x64'\n"
            f"    bootstrap_sha256='{'d' * 64}'\n"
            "    ;;\n"
            "esac\n",
            encoding="utf-8",
        )
        application = temporary_root / "application"
        helper_name = "codex-skin-helper_0.1.0-paid-alpha.11_macos_arm64"
        helper = application / "bin" / "0.1.0-paid-alpha.11" / helper_name
        helper.parent.mkdir(parents=True)
        executable(
            helper,
            "#!/bin/sh\n"
            "printf '%s\\n' '"
            + json.dumps(
                {
                    "type": "result",
                    "protocolVersion": 1,
                    "ok": True,
                    "status": "completed",
                    "data": {
                        "command": "version",
                        "helperVersion": "0.1.0-paid-alpha.11",
                        "pluginVersion": "0.1.0-paid-alpha",
                        "helperReleaseTag": "helper-v0.1.0-paid-alpha.11",
                        "apiOrigin": API_ORIGIN,
                        "buildCommit": HELPER_COMMIT,
                        "builtAt": "2026-08-03T00:00:00Z",
                    },
                    "error": None,
                },
                separators=(",", ":"),
            )
            + "'\n",
        )
        helper_sha = hashlib.sha256(helper.read_bytes()).hexdigest()
        (application / "bin" / "current.json").write_text(
            json.dumps(
                {
                    "schemaVersion": 1,
                    "helperVersion": "0.1.0-paid-alpha.11",
                    "platform": "macos-arm64",
                    "filename": helper_name,
                    "sha256": helper_sha,
                }
            )
            + "\n",
            encoding="utf-8",
        )
        recovery = application / "recovery" / "engine" / "codex-skin"
        recovery.parent.mkdir(parents=True)
        shutil.copy2(helper, recovery)
        expected_plugin_sha = attest_installation.plugin_digest(plugin)
        command = [
            "python3",
            str(ROOT / "tools" / "attest_installation.py"),
            "--plugin-root",
            str(plugin),
            "--application-root",
            str(application),
            "--platform",
            "macos-arm64",
            "--candidate-ref",
            CANDIDATE,
            "--expected-plugin-sha256",
            expected_plugin_sha,
            "--expected-api-origin",
            API_ORIGIN,
        ]
        passed = subprocess.run(command, check=False, capture_output=True, text=True)
        if passed.returncode != 0:
            raise AssertionError(passed.stdout + passed.stderr)
        report = json.loads(passed.stdout)
        if not report.get("closureMatches") or report.get("recoverySHA256") != helper_sha:
            raise AssertionError("exact installed closure did not attest")

        recovery.write_bytes(recovery.read_bytes() + b"drift")
        failed = subprocess.run(command, check=False, capture_output=True, text=True)
        if failed.returncode == 0 or "recovery engine differs" not in failed.stderr:
            raise AssertionError("recovery engine drift was not rejected")

        executable(
            helper,
            helper.read_text(encoding="utf-8").replace(
                HELPER_COMMIT, MISMATCHED_HELPER_COMMIT
            ),
        )
        helper_sha = hashlib.sha256(helper.read_bytes()).hexdigest()
        current_path = application / "bin" / "current.json"
        current = json.loads(current_path.read_text(encoding="utf-8"))
        current["sha256"] = helper_sha
        current_path.write_text(json.dumps(current) + "\n", encoding="utf-8")
        shutil.copy2(helper, recovery)
        failed = subprocess.run(command, check=False, capture_output=True, text=True)
        if failed.returncode == 0 or "version attestation does not match" not in failed.stderr:
            raise AssertionError("Helper build commit drift was not rejected")

    print("Installation attestation tests passed (exact closure + recovery/build drift).")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
