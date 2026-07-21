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


def reject_raw_json(name: str, relative: str, content: str) -> None:
    with tempfile.TemporaryDirectory(prefix="codex-skin-theme-fixture-") as directory:
        root = Path(directory)
        shutil.copytree(ROOT / "fixtures", root / "fixtures")
        target = root / "fixtures/free-test-theme-v1" / relative
        target.write_text(content, encoding="utf-8")
        result = validate(root)
        if result.returncode == 0:
            raise AssertionError(f"fixture validator accepted {name}")


def reject_tree_entry(name: str, mutate) -> None:
    with tempfile.TemporaryDirectory(prefix="codex-skin-theme-fixture-") as directory:
        root = Path(directory)
        shutil.copytree(ROOT / "fixtures", root / "fixtures")
        fixture = root / "fixtures/free-test-theme-v1"
        mutate(root, fixture)
        result = validate(root)
        if result.returncode == 0:
            raise AssertionError(f"fixture validator accepted {name}")


def main() -> int:
    if validate(ROOT).returncode:
        raise AssertionError("fixture validator rejected the reviewed fixture")
    validator_source = VALIDATOR.read_text(encoding="utf-8")
    if "zip(strict=" in validator_source:
        raise AssertionError("fixture validator must support Python 3.9 without zip(strict=...)")
    reject("manifest javascript payload", "manifest.json", lambda payload: payload.__setitem__("payload", "javascript:alert(1)"))
    reject("provenance HTTPS source", "fixture-provenance.json", lambda payload: payload.__setitem__("source", "https://evil.invalid/asset"))
    reject("manifest extra field", "manifest.json", lambda payload: payload.__setitem__("extra", True))
    reject("manifest CSS field", "manifest.json", lambda payload: payload["design"].__setitem__("css", "body{}"))
    reject("data URI token", "manifest.json", lambda payload: payload["design"]["tokens"].__setitem__("backgroundImage", "data:image/png;base64,AA=="))
    reject("schemaVersion boolean", "manifest.json", lambda payload: payload.__setitem__("schemaVersion", True))
    reject("region integer", "manifest.json", lambda payload: payload["design"]["regions"].__setitem__("home", 1))
    reject("blur float", "manifest.json", lambda payload: payload["design"]["tokens"].__setitem__("surfaceBlurPx", 18.0))
    reject("asset size float", "manifest.json", lambda payload: payload["assets"][0].__setitem__("byteSize", 670.0))
    reject_raw_json(
        "manifest duplicate JSON key",
        "manifest.json",
        '{"schemaVersion": 1, "schemaVersion": 1}',
    )
    reject_tree_entry("extra ordinary file", lambda _root, fixture: (fixture / "extra.txt").write_text("extra\n", encoding="utf-8"))
    reject_tree_entry("extra ordinary directory", lambda _root, fixture: (fixture / "extra").mkdir())
    reject_tree_entry("fixture file symlink", lambda _root, fixture: (fixture / "linked.json").symlink_to(fixture / "manifest.json"))
    reject_tree_entry("fixture directory symlink", lambda _root, fixture: (fixture / "linked-assets").symlink_to(fixture / "assets", target_is_directory=True))

    def root_symlink(root: Path, fixture: Path) -> None:
        source = root / "fixture-source"
        shutil.copytree(fixture, source)
        shutil.rmtree(fixture)
        fixture.symlink_to(source, target_is_directory=True)

    reject_tree_entry("fixture root symlink", root_symlink)
    print("Free Theme fixture exact-schema negative tests passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
