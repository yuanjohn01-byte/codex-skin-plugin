#!/usr/bin/env python3
"""Build deterministic, self-contained Skin Guardian spike artifacts."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import sys
from pathlib import Path

import build_helper as shared


ROOT = Path(__file__).resolve().parents[1]
MODULE = "github.com/yuanjohn01-byte/codex-skin-plugin"
DEFAULT_VERSION = "0.1.0-s3"
DEFAULT_OUTPUT = ROOT / "dist" / "guardian"
STRICT_SEMVER = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)"
    r"(?:\.(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*)?$"
)


def targets(version: str) -> tuple[shared.Target, ...]:
    return (
        shared.Target(
            "macos-arm64",
            "darwin",
            "arm64",
            f"codex-skin-guardian_{version}_macos_arm64",
        ),
        shared.Target(
            "macos-x64",
            "darwin",
            "amd64",
            f"codex-skin-guardian_{version}_macos_x64",
        ),
        shared.Target(
            "windows-x64",
            "windows",
            "amd64",
            f"codex-skin-guardian_{version}_windows_x64.exe",
        ),
    )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument("--version", default=DEFAULT_VERSION)
    parser.add_argument("--commit")
    parser.add_argument("--built-at")
    parser.add_argument(
        "--target",
        action="append",
        choices=[item.platform for item in targets(DEFAULT_VERSION)],
    )
    return parser.parse_args()


def build_target(
    go: str,
    target: shared.Target,
    output: Path,
    version: str,
    commit: str,
    built_at: str,
) -> dict[str, object]:
    output.mkdir(parents=True, exist_ok=True)
    destination = output / target.filename
    environment = os.environ.copy()
    environment.update(
        {
            "CGO_ENABLED": "0",
            "GOOS": target.goos,
            "GOARCH": target.goarch,
            "GOFLAGS": "-mod=readonly",
        }
    )
    ldflags = " ".join(
        (
            "-s",
            "-w",
            f"-X {MODULE}/internal/buildinfo.Version={version}",
            f"-X {MODULE}/internal/buildinfo.Commit={commit}",
            f"-X {MODULE}/internal/buildinfo.BuiltAt={built_at}",
        )
    )
    shared.run(
        [
            go,
            "build",
            "-trimpath",
            "-buildvcs=false",
            f"-ldflags={ldflags}",
            "-o",
            str(destination),
            "./cmd/codex-skin-guardian",
        ],
        env=environment,
    )
    content = destination.read_bytes()
    if str(ROOT).encode() in content:
        raise ValueError(f"{target.filename} embeds the local repository path")
    return {
        "architecture": target.goarch,
        "buildCommit": commit,
        "builtAt": built_at,
        "cgoEnabled": False,
        "component": "codex-skin-guardian",
        "filename": target.filename,
        "format": shared.binary_format(content, target),
        "goVersion": shared.REQUIRED_GO_VERSION.removeprefix("go"),
        "guardianVersion": version,
        "platform": target.platform,
        "sha256": hashlib.sha256(content).hexdigest(),
        "size": len(content),
    }


def main() -> int:
    args = parse_args()
    if not STRICT_SEMVER.fullmatch(args.version):
        raise ValueError("Guardian version must be strict SemVer without build metadata")
    commit, built_at = shared.resolve_build_metadata(args.commit, args.built_at)
    available = targets(args.version)
    selected = set(args.target or [item.platform for item in available])
    summaries = [
        build_target(
            shared.go_binary(),
            item,
            args.output.resolve(),
            args.version,
            commit,
            built_at,
        )
        for item in available
        if item.platform in selected
    ]
    summary = {"schemaVersion": 1, "artifacts": summaries}
    shared.atomic_json(args.output.resolve() / "build-summary.json", summary)
    print(json.dumps(summary, sort_keys=True))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, RuntimeError, ValueError) as exc:
        print(f"Guardian build failed: {exc}", file=sys.stderr)
        sys.exit(1)
