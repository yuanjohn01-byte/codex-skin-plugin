#!/usr/bin/env python3
"""Exercise actual Windows workflow test steps with failing native commands."""

from pathlib import Path
import os
import shutil
import subprocess
import sys
import tempfile
import textwrap


ROOT = Path(__file__).resolve().parents[1]


def test_step(workflow: str) -> str:
    marker = "      - name: Test Windows identity, theme transaction, offline restore, bootstrap, and credentials\n"
    block = workflow.split(marker, 1)[1].split("      - name:", 1)[0]
    return textwrap.dedent(block.split("        run: |\n", 1)[1])


def ps_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def main() -> int:
    shell = os.environ.get("CODEX_SKIN_TEST_PWSH") or shutil.which("pwsh")
    if shell is None:
        print("SKIP: optional PowerShell workflow execution; native Windows job runs this check")
        return 0
    script = test_step((ROOT / ".github/workflows/helper-build-spike.yml").read_text())
    with tempfile.TemporaryDirectory(prefix="codex-ci-exit-") as directory:
        root = Path(directory)
        helper = root / "dist/helper/codex-skin-helper_0.1.0-paid-alpha.17_windows_x64.exe"
        helper.parent.mkdir(parents=True)
        helper.touch()  # Resolve-Path fixture only; never executed.
        fake_go = root / "fake_go.py"
        fake_go.write_text(
            "import pathlib, sys\n"
            "p = pathlib.Path('calls.txt')\n"
            "n = int(p.read_text()) + 1 if p.exists() else 1\n"
            "p.write_text(str(n))\n"
            "sys.exit(7 if n == int(sys.argv[1]) else 0)\n"
        )
        # Run the real step, replacing only the external Go command. Mimic
        # Actions' final LASTEXITCODE forwarding, which previously hid failure.
        for failing_call, expected_calls in ((1, 1), (2, 2), (0, 2)):
            (root / "calls.txt").unlink(missing_ok=True)
            wrapper = (
                "$ErrorActionPreference = 'Stop'\n"
                "$PSNativeCommandUseErrorActionPreference = $false\n"
                f"function go {{ & {ps_literal(sys.executable)} {ps_literal(str(fake_go))} {failing_call} @args }}\n"
                + script
                + "\nexit $LASTEXITCODE\n"
            )
            result = subprocess.run(
                [shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", wrapper],
                cwd=root, capture_output=True, text=True, timeout=20,
            )
            calls = int((root / "calls.txt").read_text())
            expected_code = 7 if failing_call else 0
            if result.returncode != expected_code or calls != expected_calls:
                raise AssertionError(
                    f"native failure {failing_call}: exit={result.returncode}, calls={calls}; "
                    f"expected exit={expected_code}, calls={expected_calls}"
                )
    print("Windows CI native failure propagation passed (first/second failure and success)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
