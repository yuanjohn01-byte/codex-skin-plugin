#!/usr/bin/env python3
"""Exercise the macOS Plugin entry without touching the real user installation."""

from __future__ import annotations

import hashlib
import os
import platform
import shutil
import subprocess
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SOURCE_SCRIPTS = ROOT / "plugins" / "codex-skin" / "scripts"


def write_executable(path: Path, content: str) -> None:
    path.write_text(content, encoding="utf-8")
    path.chmod(0o700)


def run_entry(scripts: Path, home: Path, *args: str, extra: dict[str, str]) -> subprocess.CompletedProcess[str]:
    environment = {
        "HOME": str(home),
        "PATH": "/usr/bin:/bin",
        "LANG": "C.UTF-8",
        **extra,
    }
    return subprocess.run(
        [str(scripts / "codex-skin.sh"), *args],
        check=False,
        capture_output=True,
        text=True,
        env=environment,
    )


def fixture(root: Path, bootstrap_body: str) -> tuple[Path, Path, Path, Path, Path]:
    plugin = root / "plugin" / "plugins" / "codex-skin"
    scripts = plugin / "scripts"
    shutil.copytree(SOURCE_SCRIPTS, scripts)
    launcher_name = "fixture-bootstrap"
    launcher = scripts / ".bootstrap" / launcher_name
    launcher.parent.mkdir(mode=0o700)
    write_executable(launcher, bootstrap_body)
    digest = hashlib.sha256(launcher.read_bytes()).hexdigest()
    (scripts / "bootstrap-pins.sh").write_text(
        "# Generated fixture pins.\n"
        "bootstrap_release_tag='helper-v0.1.0-paid-alpha.12'\n"
        f"bootstrap_filename='{launcher_name}'\n"
        f"bootstrap_sha256='{digest}'\n",
        encoding="utf-8",
    )
    home = root / "home"
    home.mkdir()
    helper_template = root / "helper-template"
    helper_log = root / "helper.log"
    bootstrap_log = root / "bootstrap.log"
    write_executable(
        helper_template,
        "#!/bin/sh\nset -eu\nprintf '%s\\n' \"$*\" >> \"$TEST_HELPER_LOG\"\n",
    )
    return scripts, home, helper_template, helper_log, bootstrap_log


def main() -> int:
    if platform.system() != "Darwin":
        print("Plugin Bootstrap entry smoke skipped outside macOS.")
        return 0
    with tempfile.TemporaryDirectory(prefix="codex-skin-plugin-entry-") as temporary:
        root = Path(temporary)
        bootstrap = """#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$TEST_BOOTSTRAP_LOG"
destination="$HOME/Library/Application Support/CodexSkin/recovery/engine"
mkdir -p "$destination"
cp "$TEST_HELPER_TEMPLATE" "$destination/codex-skin"
chmod 700 "$destination/codex-skin"
printf '%s\n' '{"type":"result","protocolVersion":1,"ok":true,"status":"completed","data":{"helperVersion":"0.1.0-paid-alpha.12"},"error":null}'
"""
        scripts, home, helper_template, helper_log, bootstrap_log = fixture(root, bootstrap)
        extra = {
            "TEST_HELPER_TEMPLATE": str(helper_template),
            "TEST_HELPER_LOG": str(helper_log),
            "TEST_BOOTSTRAP_LOG": str(bootstrap_log),
        }
        applied = run_entry(scripts, home, "theme", "apply", "100005", "--json", extra=extra)
        if applied.returncode != 0:
            raise AssertionError(applied.stdout + applied.stderr)
        if "install --plugin-cache" not in bootstrap_log.read_text(encoding="utf-8"):
            raise AssertionError("theme apply did not run the pinned Bootstrap installer")
        if helper_log.read_text(encoding="utf-8").strip() != "theme apply 100005 --json":
            raise AssertionError("theme apply arguments did not reach the external Helper")

        bootstrap_log.unlink()
        status = run_entry(scripts, home, "status", "--json", extra=extra)
        if status.returncode != 0 or bootstrap_log.exists():
            raise AssertionError("status unexpectedly bootstrapped or failed")
        lines = helper_log.read_text(encoding="utf-8").splitlines()
        if lines[-1] != "status --json":
            raise AssertionError("status did not use the installed external Helper")

    with tempfile.TemporaryDirectory(prefix="codex-skin-plugin-entry-failure-") as temporary:
        root = Path(temporary)
        failed_bootstrap = """#!/bin/sh
printf '%s\n' '{"type":"result","protocolVersion":1,"ok":false,"status":"failed","data":null,"error":{"code":"CS-BOOTSTRAP-INSTALL-001","action":"retry_helper_install","retryable":true}}'
exit 50
"""
        scripts, home, helper_template, helper_log, bootstrap_log = fixture(root, failed_bootstrap)
        failed = run_entry(
            scripts,
            home,
            "theme",
            "apply",
            "100005",
            "--json",
            extra={
                "TEST_HELPER_TEMPLATE": str(helper_template),
                "TEST_HELPER_LOG": str(helper_log),
                "TEST_BOOTSTRAP_LOG": str(bootstrap_log),
            },
        )
        if failed.returncode != 50 or "CS-BOOTSTRAP-INSTALL-001" not in failed.stdout:
            raise AssertionError("Bootstrap failure was not returned unchanged")
        if helper_log.exists():
            raise AssertionError("Plugin invoked a Helper after Bootstrap failure")

        restore = run_entry(
            scripts,
            home,
            "theme",
            "restore",
            "--json",
            extra={
                "TEST_HELPER_TEMPLATE": str(helper_template),
                "TEST_HELPER_LOG": str(helper_log),
                "TEST_BOOTSTRAP_LOG": str(bootstrap_log),
            },
        )
        if restore.returncode != 80 or bootstrap_log.exists():
            raise AssertionError("restore without an installed Helper must fail closed without networking")

    print("Plugin Bootstrap entry smoke passed (apply bootstrap; status reuse; failure closed).")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
