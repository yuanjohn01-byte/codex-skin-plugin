#!/usr/bin/env python3
"""Build deterministic Bootstrap launchers that install signed Helper releases."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
from pathlib import Path

from build_helper import (
    MODULE,
    REQUIRED_GO_VERSION,
    ROOT,
    Target,
    atomic_json,
    binary_format,
    go_binary,
    resolve_build_metadata,
    run,
)


BOOTSTRAP_VERSION = "0.1.0-paid-alpha"
HELPER_RELEASE_TAG = "helper-v0.1.0-paid-alpha.1"
DEFAULT_OUTPUT = ROOT / "dist" / "bootstrap"
TARGETS = (
    Target(
        "macos-arm64",
        "darwin",
        "arm64",
        f"codex-skin-bootstrap_{BOOTSTRAP_VERSION}_macos_arm64",
    ),
    Target(
        "macos-x64",
        "darwin",
        "amd64",
        f"codex-skin-bootstrap_{BOOTSTRAP_VERSION}_macos_x64",
    ),
    Target(
        "windows-x64",
        "windows",
        "amd64",
        f"codex-skin-bootstrap_{BOOTSTRAP_VERSION}_windows_x64.exe",
    ),
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument("--commit")
    parser.add_argument("--built-at")
    parser.add_argument("--target", action="append", choices=[item.platform for item in TARGETS])
    return parser.parse_args()


def build_target(
    go: str,
    target: Target,
    output: Path,
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
        [
            "-s",
            "-w",
            f"-X {MODULE}/internal/buildinfo.BootstrapVersion={BOOTSTRAP_VERSION}",
            f"-X {MODULE}/internal/buildinfo.HelperReleaseTag={HELPER_RELEASE_TAG}",
            f"-X {MODULE}/internal/buildinfo.Commit={commit}",
            f"-X {MODULE}/internal/buildinfo.BuiltAt={built_at}",
        ]
    )
    run(
        [
            go,
            "build",
            "-trimpath",
            "-buildvcs=false",
            f"-ldflags={ldflags}",
            "-o",
            str(destination),
            "./cmd/codex-skin-bootstrap",
        ],
        env=environment,
    )
    content = destination.read_bytes()
    if str(ROOT).encode() in content:
        raise ValueError(f"{target.filename} embeds the local repository path")
    return {
        "architecture": target.goarch,
        "bootstrapVersion": BOOTSTRAP_VERSION,
        "buildCommit": commit,
        "builtAt": built_at,
        "cgoEnabled": False,
        "filename": target.filename,
        "format": binary_format(content, target),
        "goVersion": REQUIRED_GO_VERSION.removeprefix("go"),
        "helperReleaseTag": HELPER_RELEASE_TAG,
        "platform": target.platform,
        "sha256": hashlib.sha256(content).hexdigest(),
        "size": len(content),
    }


def main() -> int:
    args = parse_args()
    commit, built_at = resolve_build_metadata(args.commit, args.built_at)
    selected = set(args.target or [item.platform for item in TARGETS])
    summaries = [
        build_target(go_binary(), item, args.output.resolve(), commit, built_at)
        for item in TARGETS
        if item.platform in selected
    ]
    summary = {"schemaVersion": 1, "artifacts": summaries}
    atomic_json(args.output.resolve() / "build-summary.json", summary)
    print(json.dumps(summary, sort_keys=True))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, RuntimeError, ValueError) as exc:
        print(f"Bootstrap build failed: {exc}", file=sys.stderr)
        sys.exit(1)
