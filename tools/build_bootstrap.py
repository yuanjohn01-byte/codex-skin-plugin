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
from release_profiles import STAGING, profile_names, release_profile


BOOTSTRAP_VERSION = STAGING.bootstrap_version
HELPER_RELEASE_TAG = STAGING.helper_release_tag
DEFAULT_OUTPUT = ROOT / "dist" / "bootstrap"


def bootstrap_targets(version: str) -> tuple[Target, ...]:
    return (
        Target(
            "macos-arm64",
            "darwin",
            "arm64",
            f"codex-skin-bootstrap_{version}_macos_arm64",
        ),
        Target(
            "macos-x64",
            "darwin",
            "amd64",
            f"codex-skin-bootstrap_{version}_macos_x64",
        ),
        Target(
            "windows-x64",
            "windows",
            "amd64",
            f"codex-skin-bootstrap_{version}_windows_x64.exe",
        ),
    )


TARGETS = bootstrap_targets(BOOTSTRAP_VERSION)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument("--commit")
    parser.add_argument("--built-at")
    parser.add_argument("--release-profile", choices=profile_names())
    parser.add_argument("--target", action="append", choices=[item.platform for item in TARGETS])
    return parser.parse_args()


def build_target(
    go: str,
    target: Target,
    output: Path,
    commit: str,
    built_at: str,
    bootstrap_version: str,
    helper_release_tag: str,
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
            f"-X {MODULE}/internal/buildinfo.BootstrapVersion={bootstrap_version}",
            f"-X {MODULE}/internal/buildinfo.HelperReleaseTag={helper_release_tag}",
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
    if any(
        marker.encode() not in content
        for marker in (bootstrap_version, helper_release_tag)
    ):
        raise ValueError(f"{target.filename} does not embed the fixed release identity")
    return {
        "architecture": target.goarch,
        "bootstrapVersion": bootstrap_version,
        "buildCommit": commit,
        "builtAt": built_at,
        "cgoEnabled": False,
        "filename": target.filename,
        "format": binary_format(content, target),
        "goVersion": REQUIRED_GO_VERSION.removeprefix("go"),
        "helperReleaseTag": helper_release_tag,
        "platform": target.platform,
        "sha256": hashlib.sha256(content).hexdigest(),
        "size": len(content),
    }


def main() -> int:
    args = parse_args()
    commit, built_at = resolve_build_metadata(args.commit, args.built_at)
    profile = release_profile(args.release_profile) if args.release_profile else None
    bootstrap_version = profile.bootstrap_version if profile is not None else BOOTSTRAP_VERSION
    helper_release_tag = profile.helper_release_tag if profile is not None else HELPER_RELEASE_TAG
    targets = bootstrap_targets(bootstrap_version)
    selected = set(args.target or [item.platform for item in targets])
    summaries = [
        build_target(
            go_binary(),
            item,
            args.output.resolve(),
            commit,
            built_at,
            bootstrap_version,
            helper_release_tag,
        )
        for item in targets
        if item.platform in selected
    ]
    summary = {
        "schemaVersion": 1,
        "releaseProfile": profile.name if profile is not None else "review",
        "helperAPIBaseURL": profile.api_base_url if profile is not None else "",
        "artifacts": summaries,
    }
    atomic_json(args.output.resolve() / "build-summary.json", summary)
    print(json.dumps(summary, sort_keys=True))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, RuntimeError, ValueError) as exc:
        print(f"Bootstrap build failed: {exc}", file=sys.stderr)
        sys.exit(1)
