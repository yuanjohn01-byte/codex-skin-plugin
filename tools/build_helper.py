#!/usr/bin/env python3
"""Build deterministic, self-contained Codex Skin Helper test artifacts."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import struct
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
MODULE = "github.com/yuanjohn01-byte/codex-skin-plugin"
HELPER_VERSION = "0.1.0-s3"
REQUIRED_GO_VERSION = "go1.26.5"
DEFAULT_OUTPUT = ROOT / "dist" / "helper"


@dataclass(frozen=True)
class Target:
    platform: str
    goos: str
    goarch: str
    filename: str


TARGETS = (
    Target("macos-arm64", "darwin", "arm64", f"codex-skin-helper_{HELPER_VERSION}_macos_arm64"),
    Target("macos-x64", "darwin", "amd64", f"codex-skin-helper_{HELPER_VERSION}_macos_x64"),
    Target("windows-x64", "windows", "amd64", f"codex-skin-helper_{HELPER_VERSION}_windows_x64.exe"),
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument("--commit")
    parser.add_argument("--built-at")
    parser.add_argument("--target", action="append", choices=[item.platform for item in TARGETS])
    return parser.parse_args()


def run(command: list[str], *, env: dict[str, str] | None = None) -> str:
    result = subprocess.run(
        command,
        cwd=ROOT,
        env=env,
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        raise RuntimeError(result.stdout + result.stderr)
    return result.stdout.strip()


def resolve_build_metadata(commit: str | None, built_at: str | None) -> tuple[str, str]:
    resolved_commit = commit or run(["git", "rev-parse", "HEAD"])
    if not resolved_commit or len(resolved_commit) > 64:
        raise ValueError("build commit must be a non-empty identifier of at most 64 characters")
    resolved_built_at = built_at or run(["git", "show", "-s", "--format=%cI", resolved_commit])
    if not resolved_built_at or len(resolved_built_at) > 64:
        raise ValueError("build timestamp must be a non-empty value of at most 64 characters")
    return resolved_commit, resolved_built_at


def go_binary() -> str:
    executable = shutil.which("go")
    if executable is None:
        raise RuntimeError("Go is required to build the Helper")
    version = run([executable, "version"])
    if REQUIRED_GO_VERSION not in version:
        raise RuntimeError(f"expected {REQUIRED_GO_VERSION}, got {version}")
    return executable


def binary_format(content: bytes, target: Target) -> str:
    if target.goos == "darwin":
        if len(content) < 12 or content[:4] != b"\xcf\xfa\xed\xfe":
            raise ValueError(f"{target.filename} is not a 64-bit little-endian Mach-O")
        cpu_type = struct.unpack("<I", content[4:8])[0]
        expected_cpu = 0x0100000C if target.goarch == "arm64" else 0x01000007
        if cpu_type != expected_cpu:
            raise ValueError(f"{target.filename} has the wrong Mach-O architecture")
        return "mach-o-64"
    if len(content) < 64 or content[:2] != b"MZ":
        raise ValueError(f"{target.filename} is not a PE executable")
    pe_offset = struct.unpack("<I", content[0x3C:0x40])[0]
    if len(content) < pe_offset + 6 or content[pe_offset : pe_offset + 4] != b"PE\x00\x00":
        raise ValueError(f"{target.filename} has no valid PE header")
    machine = struct.unpack("<H", content[pe_offset + 4 : pe_offset + 6])[0]
    if machine != 0x8664:
        raise ValueError(f"{target.filename} is not Windows x64")
    return "pe32+-x64"


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
        (
            "-s",
            "-w",
            f"-X {MODULE}/internal/buildinfo.Version={HELPER_VERSION}",
            f"-X {MODULE}/internal/buildinfo.Commit={commit}",
            f"-X {MODULE}/internal/buildinfo.BuiltAt={built_at}",
        )
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
            "./cmd/codex-skin",
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
        "filename": target.filename,
        "format": binary_format(content, target),
        "goVersion": REQUIRED_GO_VERSION.removeprefix("go"),
        "helperVersion": HELPER_VERSION,
        "platform": target.platform,
        "sha256": hashlib.sha256(content).hexdigest(),
        "size": len(content),
    }


def atomic_json(path: Path, payload: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    content = (json.dumps(payload, indent=2, sort_keys=True) + "\n").encode()
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as handle:
        temporary = Path(handle.name)
        handle.write(content)
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, path)


def main() -> int:
    args = parse_args()
    commit, built_at = resolve_build_metadata(args.commit, args.built_at)
    selected = set(args.target or [item.platform for item in TARGETS])
    summaries = [
        build_target(go_binary(), item, args.output.resolve(), commit, built_at)
        for item in TARGETS
        if item.platform in selected
    ]
    summary = {
        "schemaVersion": 1,
        "artifacts": summaries,
    }
    atomic_json(args.output.resolve() / "build-summary.json", summary)
    print(json.dumps(summary, sort_keys=True))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, RuntimeError, ValueError) as exc:
        print(f"Helper build failed: {exc}", file=sys.stderr)
        sys.exit(1)
