#!/usr/bin/env python3
"""Exercise macOS ad-hoc signing without claiming Developer ID readiness."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_HELPER_DIR = ROOT / "dist" / "helper"
DEFAULT_OUTPUT = ROOT / "dist" / "signing" / "macos-signing-spike-summary.json"
HELPERS = (
    "codex-skin-helper_0.1.0-s3_macos_arm64",
    "codex-skin-helper_0.1.0-s3_macos_x64",
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--helper-dir", type=Path, default=DEFAULT_HELPER_DIR)
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    return parser.parse_args()


def run(command: list[str], *, check: bool = True) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(command, check=False, capture_output=True, text=True)
    if check and result.returncode != 0:
        raise RuntimeError(f"command failed ({command[0]}): {result.stderr.strip()}")
    return result


def tool_path(name: str) -> str:
    path = shutil.which(name)
    if path is None:
        raise RuntimeError(f"required macOS tool is missing: {name}")
    return path


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def atomic_json(path: Path, payload: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    content = (json.dumps(payload, indent=2, sort_keys=True) + "\n").encode()
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as handle:
        temporary = Path(handle.name)
        handle.write(content)
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, path)


def command_line_tools_version() -> str:
    result = run(["pkgutil", "--pkg-info", "com.apple.pkg.CLTools_Executables"], check=False)
    match = re.search(r"^version: (.+)$", result.stdout, re.MULTILINE)
    return match.group(1) if match else "unknown"


def developer_id_count(security: str) -> tuple[int, str]:
    result = run([security, "find-identity", "-v", "-p", "codesigning"], check=False)
    combined = result.stdout + result.stderr
    count = len(re.findall(r'"Developer ID Application:', combined))
    summary_match = re.search(r"([0-9]+) valid identities found", combined)
    valid_summary = f"{summary_match.group(1)} valid identities found" if summary_match else "unknown"
    return count, valid_summary


def main() -> int:
    args = parse_args()
    if sys.platform != "darwin":
        raise RuntimeError("macOS signing spike must run on macOS")

    codesign = tool_path("codesign")
    spctl = tool_path("spctl")
    security = tool_path("security")
    xcrun = tool_path("xcrun")
    notarytool_version = run([xcrun, "notarytool", "--version"]).stdout.strip()
    stapler_path = run([xcrun, "--find", "stapler"]).stdout.strip()
    identity_count, identity_summary = developer_id_count(security)

    results: list[dict[str, object]] = []
    with tempfile.TemporaryDirectory(prefix="codex-skin-macos-signing-") as raw_directory:
        scratch = Path(raw_directory)
        for filename in HELPERS:
            source = args.helper_dir.resolve() / filename
            if not source.is_file() or source.is_symlink():
                raise RuntimeError(f"unsigned Helper is missing or unsafe: {filename}")
            target = scratch / filename
            shutil.copyfile(source, target)
            target.chmod(0o700)

            run(
                [
                    codesign,
                    "--force",
                    "--sign",
                    "-",
                    "--timestamp=none",
                    "--identifier",
                    "com.codexskin.helper.internal-spike",
                    str(target),
                ]
            )
            run([codesign, "--verify", "--strict", "--verbose=2", str(target)])
            signature_details = run([codesign, "--display", "--verbose=4", str(target)], check=False)
            details = signature_details.stdout + signature_details.stderr
            if "Signature=adhoc" not in details:
                raise RuntimeError(f"ad-hoc signature was not reported for {filename}")

            tampered = scratch / f"{filename}.tampered"
            shutil.copyfile(target, tampered)
            with tampered.open("ab") as handle:
                handle.write(b"\0")
                handle.flush()
                os.fsync(handle.fileno())
            tamper_check = run([codesign, "--verify", "--strict", str(tampered)], check=False)
            if tamper_check.returncode == 0:
                raise RuntimeError(f"codesign accepted a tampered Helper: {filename}")

            gatekeeper = run(
                [spctl, "--assess", "--type", "execute", "--verbose=4", str(target)],
                check=False,
            )
            results.append(
                {
                    "filename": filename,
                    "signatureKind": "adhoc",
                    "codesignStrictVerified": True,
                    "tamperRejected": True,
                    "gatekeeperAccepted": gatekeeper.returncode == 0,
                    "signedSha256": sha256(target),
                }
            )

    summary = {
        "schemaVersion": 1,
        "scope": "internal-ad-hoc-feasibility-only",
        "formalDistributionReady": False,
        "notaryUploadAttempted": False,
        "developerIdApplicationIdentityCount": identity_count,
        "codesigningIdentitySummary": identity_summary,
        "tools": {
            "commandLineToolsVersion": command_line_tools_version(),
            "notarytoolVersion": notarytool_version,
            "staplerAvailable": bool(stapler_path),
        },
        "artifacts": results,
        "limitations": [
            "ad-hoc signatures are not Developer ID signatures",
            "no notarization submission or ticket was created",
            "standalone Mach-O executables cannot carry a stapled ticket",
            "Gatekeeper acceptance from this spike is not a public-distribution claim",
        ],
    }
    atomic_json(args.output.resolve(), summary)
    print(json.dumps(summary, sort_keys=True))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, RuntimeError, ValueError) as exc:
        print(f"macOS signing spike failed: {exc}", file=sys.stderr)
        sys.exit(1)
