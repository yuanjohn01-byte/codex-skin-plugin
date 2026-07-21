#!/usr/bin/env python3
"""Fail closed unless the exported Free Theme fixture is the exact reviewed data set."""

from __future__ import annotations

import argparse
import hashlib
import json
import struct
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
FIXTURE = Path("fixtures/free-test-theme-v1")
ASSET_PATH = "assets/synthetic-dawn.png"
ASSET_SHA256 = "32778aa571beb986c74d682ef5711dc5b8f412332538efa8e277ace8c5e41575"
EXPECTED_POLICY = {
    "schemaVersion": 1,
    "requiredRegions": ["home", "sidebar", "suggestionCards", "projectPicker", "composer"],
    "allowedTokens": ["backgroundImage", "backgroundOverlay", "surfaceOpacity", "surfaceBlurPx", "textPrimary", "textSecondary", "accent", "border", "radiusScale"],
    "allowedCustomization": ["backgroundOverlay", "surfaceOpacity", "surfaceBlurPx", "accent", "radiusScale"],
    "allowedAssetPaths": [ASSET_PATH],
    "allowedAssetRoles": ["background"],
    "allowedAssetContentTypes": ["image/png"],
    "requiredPlatforms": ["macos", "windows"],
}
EXPECTED_PROVENANCE = {
    "schemaVersion": 1,
    "asset": ASSET_PATH,
    "rights": "Synthetic asset generated deterministically in this repository; no third-party or source artwork is used.",
    "generator": "tools/codex-skin/generate_free_theme_fixture_asset.py",
    "generatorVersion": 1,
    "description": "A 16-by-16 RGB gradient assembled from fixed arithmetic constants and encoded as a standard PNG.",
}
EXPECTED_MANIFEST = {
    "schemaVersion": 1,
    "themePublicId": "100001",
    "themeVersion": "1.0.0",
    "name": "Synthetic Dawn",
    "design": {
        "mode": "dark",
        "tokens": {
            "backgroundImage": ASSET_PATH,
            "backgroundOverlay": 0.42,
            "surfaceOpacity": 0.82,
            "surfaceBlurPx": 18,
            "textPrimary": "#F7F7FA",
            "textSecondary": "#C8CAD3",
            "accent": "#A78BFA",
            "border": "#FFFFFF24",
            "radiusScale": 1.0,
        },
        "regions": {"home": True, "sidebar": True, "suggestionCards": True, "projectPicker": True, "composer": True},
    },
    "customization": {"allowed": ["backgroundOverlay", "surfaceOpacity", "surfaceBlurPx", "accent", "radiusScale"]},
    "assets": [{"path": ASSET_PATH, "role": "background", "contentType": "image/png", "byteSize": 670, "sha256": ASSET_SHA256}],
    "compatibility": {"platforms": ["macos", "windows"], "minEngineVersion": "0.1.0"},
}


def load_json(path: Path) -> dict[str, object]:
    payload = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(payload, dict):
        raise ValueError(f"{path.name} must be a JSON object")
    return payload


def validate_png(path: Path) -> bytes:
    content = path.read_bytes()
    if content[:8] != b"\x89PNG\r\n\x1a\n" or len(content) < 33:
        raise ValueError("asset must be a PNG")
    if struct.unpack(">I", content[8:12])[0] != 13 or content[12:16] != b"IHDR":
        raise ValueError("asset PNG IHDR is invalid")
    if struct.unpack(">IIBBBBB", content[16:29]) != (16, 16, 8, 2, 0, 0, 0):
        raise ValueError("asset PNG must be the tracked 16x16 RGB synthetic image")
    return content


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=ROOT)
    parser.add_argument("--fixture", type=Path, default=FIXTURE)
    args = parser.parse_args()
    fixture = args.root.resolve() / args.fixture
    allowed_files = {"fixture-policy-v1.json", "fixture-provenance.json", "manifest.json", ASSET_PATH}
    actual_files = {path.relative_to(fixture).as_posix() for path in fixture.rglob("*") if path.is_file()}
    if actual_files != allowed_files:
        raise ValueError(f"fixture must contain only reviewed JSON and the synthetic PNG: {sorted(actual_files)}")
    if load_json(fixture / "fixture-policy-v1.json") != EXPECTED_POLICY:
        raise ValueError("fixture policy does not match the exact reviewed v1 schema")
    if load_json(fixture / "fixture-provenance.json") != EXPECTED_PROVENANCE:
        raise ValueError("fixture provenance does not match the exact reviewed schema")
    if load_json(fixture / "manifest.json") != EXPECTED_MANIFEST:
        raise ValueError("fixture manifest does not match the exact reviewed v1 data-only structure")
    content = validate_png(fixture / ASSET_PATH)
    if hashlib.sha256(content).hexdigest() != ASSET_SHA256:
        raise ValueError("fixture asset SHA-256 does not match its reviewed bytes")
    print("Free Theme Manifest v1 fixture validation passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
