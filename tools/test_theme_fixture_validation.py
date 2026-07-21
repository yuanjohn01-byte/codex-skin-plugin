#!/usr/bin/env python3
"""Regression tests for exact, fail-closed Free Theme fixture validation."""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
VALIDATOR = ROOT / "tools/test_theme_fixture.py"


def validate(root: Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run([sys.executable, str(VALIDATOR), "--root", str(root)], check=False, capture_output=True, text=True)


def reject(name: str, relative: str, mutate) -> None:
    with tempfile.TemporaryDirectory(prefix="codex-skin-theme-fixture-") as directory:
        root = Path(directory)
        shutil.copytree(ROOT / "fixtures", root / "fixtures")
        target = root / "fixtures/free-test-theme-v1" / relative
        payload = json.loads(target.read_text(encoding="utf-8"))
        mutate(payload)
        target.write_text(json.dumps(payload), encoding="utf-8")
        result = validate(root)
        if result.returncode == 0:
            raise AssertionError(f"fixture validator accepted {name}")


def main() -> int:
    if validate(ROOT).returncode:
        raise AssertionError("fixture validator rejected the reviewed fixture")
    reject("manifest javascript payload", "manifest.json", lambda payload: payload.__setitem__("payload", "javascript:alert(1)"))
    reject("provenance HTTPS source", "fixture-provenance.json", lambda payload: payload.__setitem__("source", "https://evil.invalid/asset"))
    reject("manifest extra field", "manifest.json", lambda payload: payload.__setitem__("extra", True))
    reject("manifest CSS field", "manifest.json", lambda payload: payload["design"].__setitem__("css", "body{}"))
    reject("data URI token", "manifest.json", lambda payload: payload["design"]["tokens"].__setitem__("backgroundImage", "data:image/png;base64,AA=="))
    print("Free Theme fixture exact-schema negative tests passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
