#!/usr/bin/env python3
"""Create the public SPDX SBOM that accompanies a Helper release."""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

from release_profiles import PROFILES, STAGING, profile_names, release_profile


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_SUMMARY = ROOT / "dist" / "helper" / "build-summary.json"
REPOSITORY = "yuanjohn01-byte/codex-skin-plugin"
VERSION = STAGING.helper_version
DIGEST = re.compile(r"^[0-9a-f]{64}$")
PLATFORMS = ("macos-arm64", "macos-x64", "windows-x64")
SUFFIXES = {
    "macos-arm64": "macos_arm64",
    "macos-x64": "macos_x64",
    "windows-x64": "windows_x64.exe",
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--summary", type=Path, default=DEFAULT_SUMMARY)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--release-profile", choices=profile_names())
    return parser.parse_args()


def atomic_json(path: Path, payload: dict[str, Any]) -> None:
    content = json.dumps(payload, indent=2, sort_keys=True) + "\n"
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", dir=path.parent, delete=False) as handle:
        temporary = Path(handle.name)
        handle.write(content)
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, path)


def load_summary(
    path: Path,
    profile_name: str | None = None,
) -> tuple[list[dict[str, Any]], str, str, str, str]:
    payload = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(payload, dict) or payload.get("schemaVersion") != 1:
        raise ValueError("Helper build summary must use schemaVersion 1")
    profile = release_profile(profile_name) if profile_name is not None else None
    declared_profile = payload.get("releaseProfile")
    if profile is not None:
        if declared_profile != profile.name or payload.get("apiBaseURL") != profile.api_base_url:
            raise ValueError("Helper build summary does not match the fixed release profile")
    elif declared_profile in PROFILES:
        raise ValueError("a protected Helper build summary requires an explicit release profile")
    artifacts = payload.get("artifacts")
    if not isinstance(artifacts, list) or len(artifacts) != len(PLATFORMS):
        raise ValueError("Helper build summary must contain exactly three artifacts")
    ordered: list[dict[str, Any]] = []
    found: set[str] = set()
    commit = ""
    built_at = ""
    version = ""
    for item in artifacts:
        if not isinstance(item, dict):
            raise ValueError("Helper build artifact must be an object")
        platform = item.get("platform")
        filename = item.get("filename")
        digest = item.get("sha256")
        size = item.get("size")
        item_version = item.get("helperVersion")
        if (
            platform not in PLATFORMS
            or platform in found
            or not isinstance(item_version, str)
            or not item_version
            or not isinstance(filename, str)
            or filename != f"codex-skin-helper_{item_version}_{SUFFIXES.get(platform, '')}"
            or not isinstance(digest, str)
            or DIGEST.fullmatch(digest) is None
            or not isinstance(size, int)
            or isinstance(size, bool)
            or size < 1
            or not isinstance(item.get("buildCommit"), str)
            or not item["buildCommit"]
            or not isinstance(item.get("builtAt"), str)
            or not item["builtAt"]
        ):
            raise ValueError("Helper build artifact does not meet the release contract")
        if profile is not None and item_version != profile.helper_version:
            raise ValueError("Helper build artifact version does not match the fixed release profile")
        if profile is not None and item.get("helperReleaseTag") != profile.helper_release_tag:
            raise ValueError("Helper build artifact tag does not match the fixed release profile")
        if not version:
            version = item_version
        elif item_version != version:
            raise ValueError("Helper artifacts must use one exact version")
        if not commit:
            commit = item["buildCommit"]
            built_at = item["builtAt"]
        if item["buildCommit"] != commit or item["builtAt"] != built_at:
            raise ValueError("Helper artifacts must share exact build provenance")
        found.add(platform)
        ordered.append(item)
    if found != set(PLATFORMS):
        raise ValueError("Helper build summary platform set is incomplete")
    profile_label = profile.name if profile is not None else "review"
    return sorted(ordered, key=lambda item: str(item["platform"])), commit, built_at, version, profile_label


def go_modules() -> list[dict[str, str]]:
    completed = subprocess.run(
        ["go", "list", "-m", "-json", "all"],
        cwd=ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    decoder = json.JSONDecoder()
    raw = completed.stdout
    index = 0
    modules: list[dict[str, str]] = []
    while index < len(raw):
        while index < len(raw) and raw[index].isspace():
            index += 1
        if index == len(raw):
            break
        item, index = decoder.raw_decode(raw, index)
        if not isinstance(item, dict) or not isinstance(item.get("Path"), str):
            raise ValueError("Go module metadata is invalid")
        modules.append(
            {
                "path": item["Path"],
                "version": item.get("Version") if isinstance(item.get("Version"), str) else "NOASSERTION",
            }
        )
    if not modules or modules[0]["path"] != "github.com/yuanjohn01-byte/codex-skin-plugin":
        raise ValueError("Go module root is not the Codex Skin Plugin")
    return modules


def spdx_id(prefix: str, value: str) -> str:
    return "SPDXRef-" + prefix + "-" + re.sub(r"[^A-Za-z0-9.-]", "-", value)


def create(summary: Path, profile_name: str | None = None) -> dict[str, Any]:
    artifacts, commit, built_at, version, profile_label = load_summary(summary, profile_name)
    release_tag = f"helper-v{version}"
    root_id = "SPDXRef-Package-codex-skin-helper"
    packages: list[dict[str, Any]] = [
        {
            "SPDXID": root_id,
            "downloadLocation": "NOASSERTION",
            "filesAnalyzed": False,
            "name": "codex-skin-helper",
            "supplier": "Organization: Codex Skin",
            "versionInfo": version,
        }
    ]
    relationships: list[dict[str, str]] = [{
        "spdxElementId": "SPDXRef-DOCUMENT", "relationshipType": "DESCRIBES", "relatedSpdxElement": root_id,
    }]
    for module in go_modules()[1:]:
        module_id = spdx_id("Package", module["path"])
        packages.append(
            {
                "SPDXID": module_id,
                "downloadLocation": f"https://{module['path']}",
                "filesAnalyzed": False,
                "name": module["path"],
                "versionInfo": module["version"],
            }
        )
        relationships.append(
            {"spdxElementId": root_id, "relationshipType": "DEPENDS_ON", "relatedSpdxElement": module_id}
        )
    files: list[dict[str, Any]] = []
    for artifact in artifacts:
        file_id = spdx_id("File", str(artifact["platform"]))
        files.append(
            {
                "SPDXID": file_id,
                "checksums": [{"algorithm": "SHA256", "checksumValue": artifact["sha256"]}],
                "fileName": artifact["filename"],
            }
        )
        relationships.append(
            {"spdxElementId": root_id, "relationshipType": "CONTAINS", "relatedSpdxElement": file_id}
        )
    profiled = profile_name is not None
    return {
        "SPDXID": "SPDXRef-DOCUMENT",
        "creationInfo": {
            "created": built_at,
            "creators": ["Tool: codex-skin-create-sbom"],
        },
        "dataLicense": "CC0-1.0",
        "documentNamespace": (
            f"https://github.com/{REPOSITORY}/releases/{release_tag}/sbom/{profile_label}/{commit}"
            if profiled
            else f"https://github.com/{REPOSITORY}/releases/{release_tag}/sbom/{commit}"
        ),
        "files": files,
        "name": (
            f"codex-skin-helper-{version}-{profile_label}-sbom"
            if profiled
            else f"codex-skin-helper-{version}-sbom"
        ),
        "packages": packages,
        "relationships": relationships,
        "spdxVersion": "SPDX-2.3",
    }


def main() -> int:
    args = parse_args()
    document = create(args.summary, args.release_profile)
    version = document["packages"][0]["versionInfo"]
    suffix = f"_{args.release_profile}" if args.release_profile else ""
    output = args.output or args.summary.parent / f"codex-skin-helper_{version}{suffix}.sbom.spdx.json"
    atomic_json(output, document)
    print(output)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, RuntimeError, ValueError, subprocess.CalledProcessError) as exc:
        print(f"SBOM build failed: {exc}", file=sys.stderr)
        sys.exit(1)
