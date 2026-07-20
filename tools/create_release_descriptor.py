#!/usr/bin/env python3
"""Create unsigned canonical Helper release metadata from a trusted build summary."""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import tempfile
from datetime import datetime, timezone
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_SUMMARY = ROOT / "dist" / "helper" / "build-summary.json"
DEFAULT_OUTPUT = ROOT / "dist" / "helper" / "release-descriptor.json"
PLATFORMS = ("macos-arm64", "macos-x64", "windows-x64")
STRICT_SEMVER = re.compile(
    r"^(0|[1-9][0-9]*)\."
    r"(0|[1-9][0-9]*)\."
    r"(0|[1-9][0-9]*)"
    r"(?:-(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
    r"(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$"
)
KEY_ID = re.compile(r"^[a-z0-9][a-z0-9-]{2,63}$")
DIGEST = re.compile(r"^[0-9a-f]{64}$")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--summary", type=Path, default=DEFAULT_SUMMARY)
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument("--key-id", required=True)
    return parser.parse_args()


def expected_filename(version: str, platform: str) -> str:
    suffix = {
        "macos-arm64": "macos_arm64",
        "macos-x64": "macos_x64",
        "windows-x64": "windows_x64.exe",
    }[platform]
    return f"codex-skin-helper_{version}_{suffix}"


def utc_timestamp(value: object) -> str:
    if not isinstance(value, str) or len(value) > 64:
        raise ValueError("build timestamp must be a short RFC 3339 string")
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        raise ValueError("build timestamp must contain an offset")
    normalized = parsed.astimezone(timezone.utc).replace(microsecond=0)
    return normalized.isoformat().replace("+00:00", "Z")


def descriptor_from_summary(summary: object, key_id: str) -> dict[str, object]:
    if not KEY_ID.fullmatch(key_id):
        raise ValueError("key id must match the public lowercase identifier contract")
    if not isinstance(summary, dict) or summary.get("schemaVersion") != 1:
        raise ValueError("build summary must use schemaVersion 1")
    artifacts = summary.get("artifacts")
    if not isinstance(artifacts, list) or len(artifacts) != len(PLATFORMS):
        raise ValueError("build summary must contain exactly three artifacts")

    descriptor_artifacts: list[dict[str, object]] = []
    version: str | None = None
    published_at: str | None = None
    for index, item in enumerate(artifacts):
        if not isinstance(item, dict):
            raise ValueError("build artifact must be an object")
        platform = item.get("platform")
        if platform != PLATFORMS[index]:
            raise ValueError("build artifacts must use the fixed platform order")
        item_version = item.get("helperVersion")
        if not isinstance(item_version, str) or STRICT_SEMVER.fullmatch(item_version) is None:
            raise ValueError("build artifact helperVersion must be strict SemVer without metadata")
        if version is None:
            version = item_version
        elif version != item_version:
            raise ValueError("all build artifacts must use one Helper version")
        item_timestamp = utc_timestamp(item.get("builtAt"))
        if published_at is None:
            published_at = item_timestamp
        elif published_at != item_timestamp:
            raise ValueError("all build artifacts must use one build timestamp")
        filename = item.get("filename")
        if filename != expected_filename(item_version, platform):
            raise ValueError("build artifact filename does not match version and platform")
        digest = item.get("sha256")
        if not isinstance(digest, str) or DIGEST.fullmatch(digest) is None:
            raise ValueError("build artifact sha256 must be lowercase hexadecimal")
        size = item.get("size")
        if not isinstance(size, int) or isinstance(size, bool) or not 1 <= size <= 50 * 1024 * 1024:
            raise ValueError("build artifact size is outside the public limit")
        descriptor_artifacts.append(
            {
                "platform": platform,
                "filename": filename,
                "sha256": digest,
                "size": size,
            }
        )

    if version is None or published_at is None:
        raise ValueError("build summary is empty")
    return {
        "schemaVersion": 1,
        "helperVersion": version,
        "releaseTag": f"helper-v{version}",
        "publishedAt": published_at,
        "signingKeyId": key_id,
        "artifacts": descriptor_artifacts,
    }


def canonical_bytes(descriptor: object) -> bytes:
    return (json.dumps(descriptor, ensure_ascii=False, separators=(",", ":")) + "\n").encode()


def atomic_write(path: Path, content: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as handle:
        temporary = Path(handle.name)
        handle.write(content)
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, path)


def main() -> int:
    args = parse_args()
    summary = json.loads(args.summary.read_text(encoding="utf-8"))
    descriptor = descriptor_from_summary(summary, args.key_id)
    atomic_write(args.output, canonical_bytes(descriptor))
    print(json.dumps(descriptor, sort_keys=True))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"Release descriptor generation failed: {exc}", file=sys.stderr)
        sys.exit(1)
