#!/usr/bin/env python3
"""Exercise the signed per-user Guardian lifecycle on a macOS runner."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import plistlib
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_OUTPUT = ROOT / "dist" / "guardian" / "macos-lifecycle-summary.json"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--v1", type=Path, required=True)
    parser.add_argument("--v2", type=Path, required=True)
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    return parser.parse_args()


def run(command: list[str], *, env: dict[str, str] | None = None, check: bool = True) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(command, cwd=ROOT, env=env, check=False, capture_output=True, text=True)
    if check and result.returncode != 0:
        raise RuntimeError(f"command failed ({command[0]}): {result.stderr.strip()}")
    return result


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def atomic_json(path: Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    content = (json.dumps(value, indent=2, sort_keys=True) + "\n").encode()
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as handle:
        temporary = Path(handle.name)
        handle.write(content)
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, path)


def main() -> int:
    args = parse_args()
    if sys.platform != "darwin":
        raise RuntimeError("macOS Guardian lifecycle must run on macOS")
    codesign = shutil.which("codesign")
    launchctl = shutil.which("launchctl")
    go = shutil.which("go")
    if not codesign or not launchctl or not go:
        raise RuntimeError("codesign, launchctl, and Go are required")

    label = f"com.codexskin.guardian.internal-spike.{os.getpid()}"
    domain = f"gui/{os.getuid()}"
    service = f"{domain}/{label}"
    registered = False
    summary: dict[str, object] | None = None
    with tempfile.TemporaryDirectory(prefix="codex-skin-guardian-macos-") as raw_directory:
        scratch = Path(raw_directory)
        signed: list[Path] = []
        for index, source in enumerate((args.v1.resolve(), args.v2.resolve()), start=1):
            if not source.is_file() or source.is_symlink():
                raise RuntimeError(f"Guardian v{index} is missing or unsafe")
            target = scratch / f"guardian-v{index}"
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
                    "com.codexskin.guardian.internal-spike",
                    str(target),
                ]
            )
            run([codesign, "--verify", "--strict", "--verbose=2", str(target)])
            signed.append(target)

        tampered = scratch / "guardian-v2-tampered"
        shutil.copyfile(signed[1], tampered)
        with tampered.open("ab") as handle:
            handle.write(b"\0")
            handle.flush()
            os.fsync(handle.fileno())
        if run([codesign, "--verify", "--strict", str(tampered)], check=False).returncode == 0:
            raise RuntimeError("codesign accepted a tampered Guardian")

        test_environment = os.environ.copy()
        test_environment["CODEX_SKIN_TEST_GUARDIAN_V1"] = str(signed[0])
        test_environment["CODEX_SKIN_TEST_GUARDIAN_V2"] = str(signed[1])
        run([go, "test", "./internal/guardian", "-run", "TestNativeGuardianLifecycle", "-count=1", "-v"], env=test_environment)

        minimal_environment = {"PATH": "/usr/bin:/bin", "LANG": "C.UTF-8"}
        version = run([str(signed[0]), "version", "--json"], env=minimal_environment).stdout
        if json.loads(version).get("guardianVersion") != "0.1.0-s3":
            raise RuntimeError("signed Guardian version contract failed")

        plist_path = scratch / f"{label}.plist"
        plist = {
            "Label": label,
            "ProgramArguments": [
                str(signed[0]),
                "run",
                "--reason",
                "process",
                "--json",
                "--internal-spike",
            ],
            "RunAtLoad": True,
            "KeepAlive": False,
            "ProcessType": "Background",
            "LimitLoadToSessionType": "Aqua",
        }
        with plist_path.open("wb") as handle:
            plistlib.dump(plist, handle, fmt=plistlib.FMT_XML, sort_keys=True)
        run(["plutil", "-lint", str(plist_path)])
        try:
            run([launchctl, "bootstrap", domain, str(plist_path)])
            registered = True
            run([launchctl, "print", service])
            run([launchctl, "kickstart", "-k", service], check=False)
        finally:
            if registered:
                run([launchctl, "bootout", service])
                registered = False
        if run([launchctl, "print", service], check=False).returncode == 0:
            raise RuntimeError("LaunchAgent registration remained after bootout")

        summary = {
            "schemaVersion": 1,
            "scope": "internal-per-user-guardian-feasibility-only",
            "platform": "macos",
            "formalDistributionReady": False,
            "signatureKind": "adhoc",
            "codesignStrictVerified": True,
            "tamperRejected": True,
            "nativeInstallUpgradeRollbackUninstall": True,
            "perUserRegistrationInstalled": True,
            "perUserRegistrationRemoved": True,
            "requiresAdministratorAtRuntime": False,
            "networkListener": False,
            "arbitraryCommandSurface": False,
            "signedSha256": [sha256(path) for path in signed],
            "limitations": [
                "ad-hoc signing is not Developer ID signing or notarization",
                "the fixed trigger validates packaging only; lifecycle reconcile is deferred to M1-S6-013",
            ],
        }

    if summary is None:
        raise RuntimeError("macOS Guardian summary was not produced")
    atomic_json(args.output.resolve(), summary)
    print(json.dumps(summary, sort_keys=True))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, RuntimeError, ValueError, json.JSONDecodeError) as exc:
        print(f"macOS Guardian lifecycle failed: {exc}", file=sys.stderr)
        sys.exit(1)
