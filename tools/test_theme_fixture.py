#!/usr/bin/env python3
"""Validate the exported synthetic Free Theme Manifest v1 fixture without executing it."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import struct
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
FIXTURE = Path("fixtures/free-test-theme-v1")
SEMVER = re.compile(r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$")
PUBLIC_ID = re.compile(r"^\d{6}$")
BANNED_KEY_PARTS = ("css", "command", "html", "javascript", "powershell", "script", "selector", "shell", "url", "xpath")


def load_json(path: Path) -> dict[str, object]:
    payload = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(payload, dict):
        raise ValueError(f"{path.name} must be a JSON object")
    return payload


def reject_executable_values(value: object) -> None:
    if isinstance(value, dict):
        for key, item in value.items():
            if not isinstance(key, str) or any(part in key.lower() for part in BANNED_KEY_PARTS):
                raise ValueError(f"forbidden executable or remote field: {key!r}")
            reject_executable_values(item)
    elif isinstance(value, list):
        for item in value:
            reject_executable_values(item)
    elif isinstance(value, str) and ("://" in value or value.startswith("data:")):
        raise ValueError("fixture must not contain a URL or data URL")


def validate_png(path: Path) -> None:
    content = path.read_bytes()
    if content[:8] != b"\x89PNG\r\n\x1a\n" or len(content) < 33:
        raise ValueError("asset must be a PNG")
    length = struct.unpack(">I", content[8:12])[0]
    if content[12:16] != b"IHDR" or length != 13:
        raise ValueError("asset PNG IHDR is invalid")
    details = struct.unpack(">IIBBBBB", content[16:29])
    if details != (16, 16, 8, 2, 0, 0, 0):
        raise ValueError("asset PNG must be the tracked 16x16 RGB synthetic image")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=ROOT)
    parser.add_argument("--fixture", type=Path, default=FIXTURE)
    args = parser.parse_args()
    fixture = args.root.resolve() / args.fixture
    allowed_files = {"fixture-policy-v1.json", "fixture-provenance.json", "manifest.json", "assets/synthetic-dawn.png"}
    actual_files = {path.relative_to(fixture).as_posix() for path in fixture.rglob("*") if path.is_file()}
    if actual_files != allowed_files:
        raise ValueError(f"fixture must contain only allowlisted JSON and the synthetic PNG: {sorted(actual_files)}")
    policy = load_json(fixture / "fixture-policy-v1.json")
    provenance = load_json(fixture / "fixture-provenance.json")
    manifest = load_json(fixture / "manifest.json")
    reject_executable_values(manifest)
    if policy.get("schemaVersion") != 1 or provenance.get("schemaVersion") != 1 or manifest.get("schemaVersion") != 1:
        raise ValueError("fixture policy, provenance, and manifest must use schemaVersion 1")
    if "Synthetic asset generated deterministically" not in str(provenance.get("rights")) or provenance.get("asset") != "assets/synthetic-dawn.png":
        raise ValueError("fixture provenance must state synthetic rights and name the only asset")
    if manifest.get("themePublicId") != "100001" or not PUBLIC_ID.fullmatch(str(manifest.get("themePublicId"))):
        raise ValueError("fixture themePublicId must be the six-digit Free test ID 100001")
    if not isinstance(manifest.get("themeVersion"), str) or not SEMVER.fullmatch(manifest["themeVersion"]):
        raise ValueError("fixture themeVersion must be SemVer")
    design, customization, compatibility, assets = (manifest.get("design"), manifest.get("customization"), manifest.get("compatibility"), manifest.get("assets"))
    if not all(isinstance(value, dict) for value in (design, customization, compatibility)) or not isinstance(assets, list):
        raise ValueError("fixture manifest sections are invalid")
    assert isinstance(design, dict) and isinstance(customization, dict) and isinstance(compatibility, dict)
    tokens, regions = design.get("tokens"), design.get("regions")
    if not isinstance(tokens, dict) or set(tokens) != set(policy["allowedTokens"]):
        raise ValueError("fixture tokens must equal the Manifest v1 allowlist")
    if not isinstance(regions, dict) or set(regions) != set(policy["requiredRegions"]) or any(value is not True for value in regions.values()):
        raise ValueError("fixture must enable exactly the five Manifest v1 regions")
    if customization.get("allowed") != policy["allowedCustomization"]:
        raise ValueError("fixture customization allowlist is invalid")
    if compatibility.get("platforms") != policy["requiredPlatforms"] or not SEMVER.fullmatch(str(compatibility.get("minEngineVersion"))):
        raise ValueError("fixture compatibility must declare macOS, Windows, and a SemVer minimum engine")
    if len(assets) != 1 or not isinstance(assets[0], dict):
        raise ValueError("fixture must have exactly one safe image asset")
    asset = assets[0]
    if asset.get("path") not in policy["allowedAssetPaths"] or asset.get("role") not in policy["allowedAssetRoles"] or asset.get("contentType") not in policy["allowedAssetContentTypes"]:
        raise ValueError("fixture asset metadata is outside the allowlist")
    image_path = fixture / str(asset.get("path"))
    validate_png(image_path)
    content = image_path.read_bytes()
    if asset.get("byteSize") != len(content) or asset.get("sha256") != hashlib.sha256(content).hexdigest():
        raise ValueError("fixture asset size or SHA-256 does not match its bytes")
    print("Free Theme Manifest v1 fixture validation passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
