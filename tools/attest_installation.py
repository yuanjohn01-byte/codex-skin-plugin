#!/usr/bin/env python3
"""Fail-closed attestation for an installed Plugin/Bootstrap/Helper closure."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
from pathlib import Path


SHA256 = re.compile(r"^[0-9a-f]{64}$")
GIT_SHA = re.compile(r"^[0-9a-f]{40}$")
SEMVER = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$")


def arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--plugin-root", required=True, type=Path)
    parser.add_argument("--application-root", required=True, type=Path)
    parser.add_argument("--platform", required=True, choices=("macos-arm64", "macos-x64", "windows-x64"))
    parser.add_argument("--candidate-ref", required=True)
    parser.add_argument("--expected-plugin-sha256", required=True)
    parser.add_argument("--expected-api-origin", required=True)
    return parser.parse_args()


def regular_bytes(path: Path, maximum: int) -> bytes:
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"unsafe or missing regular file: {path.name}")
    size = path.stat().st_size
    if not 1 <= size <= maximum:
        raise ValueError(f"file size outside attestation bound: {path.name}")
    content = path.read_bytes()
    if len(content) != size:
        raise ValueError(f"file changed while attesting: {path.name}")
    return content


def digest(content: bytes) -> str:
    return hashlib.sha256(content).hexdigest()


def plugin_digest(plugin_root: Path) -> str:
    hasher = hashlib.sha256()
    count = 0
    for path in sorted(plugin_root.rglob("*")):
        relative = path.relative_to(plugin_root).as_posix()
        if relative == "scripts/.bootstrap" or relative.startswith("scripts/.bootstrap/"):
            continue
        if path.is_symlink():
            raise ValueError(f"Plugin contains a symbolic link: {relative}")
        if path.is_dir():
            continue
        content = regular_bytes(path, 8 * 1024 * 1024)
        encoded = relative.encode("utf-8")
        hasher.update(len(encoded).to_bytes(4, "big"))
        hasher.update(encoded)
        hasher.update(len(content).to_bytes(8, "big"))
        hasher.update(content)
        count += 1
    if count == 0:
        raise ValueError("Plugin root contains no files")
    return hasher.hexdigest()


def shell_pins(path: Path, platform_name: str) -> dict[str, str]:
    text = regular_bytes(path, 32 * 1024).decode("utf-8")
    values: dict[str, str] = {}
    for key in ("bootstrap_release_tag", "bootstrap_version", "bootstrap_build_commit", "bootstrap_built_at"):
        matches = re.findall(rf"^{key}='([^']+)'$", text, re.MULTILINE)
        if len(matches) != 1:
            raise ValueError(f"missing exact Bootstrap pin: {key}")
        values[key] = matches[0]
    suffix = "macos_arm64" if platform_name == "macos-arm64" else "macos_x64"
    filename = f"codex-skin-bootstrap_{values['bootstrap_version']}_{suffix}"
    if text.count(f"bootstrap_filename='{filename}'") != 1:
        raise ValueError("platform Bootstrap filename pin is missing")
    block = text.split(f"bootstrap_filename='{filename}'", 1)[1]
    match = re.search(r"bootstrap_sha256='([0-9a-f]{64})'", block)
    if match is None:
        raise ValueError("platform Bootstrap digest pin is missing")
    values["bootstrap_filename"] = filename
    values["bootstrap_sha256"] = match.group(1)
    return values


def powershell_pins(path: Path) -> dict[str, str]:
    text = regular_bytes(path, 32 * 1024).decode("utf-8")
    names = {
        "bootstrap_release_tag": "bootstrapReleaseTag",
        "bootstrap_version": "bootstrapVersion",
        "bootstrap_build_commit": "bootstrapBuildCommit",
        "bootstrap_built_at": "bootstrapBuiltAt",
        "bootstrap_filename": "bootstrapFilename",
        "bootstrap_sha256": "bootstrapSHA256",
    }
    values: dict[str, str] = {}
    for key, variable in names.items():
        matches = re.findall(rf'^\${variable} = "([^"]+)"$', text, re.MULTILINE)
        if len(matches) != 1:
            raise ValueError(f"missing exact Bootstrap pin: {variable}")
        values[key] = matches[0]
    return values


def helper_version(executable: Path) -> dict[str, object]:
    completed = subprocess.run(
        [str(executable), "version", "--json"],
        check=False,
        capture_output=True,
        text=True,
        env={"PATH": os.environ.get("PATH", "")},
        timeout=10,
    )
    if completed.returncode != 0:
        raise ValueError("installed Helper version command failed")
    result = json.loads(completed.stdout)
    if result.get("type") != "result" or result.get("ok") is not True or result.get("status") != "completed":
        raise ValueError("installed Helper returned an invalid version result")
    data = result.get("data")
    if not isinstance(data, dict) or data.get("command") != "version":
        raise ValueError("installed Helper version data is invalid")
    return data


def main() -> int:
    args = arguments()
    if GIT_SHA.fullmatch(args.candidate_ref) is None or SHA256.fullmatch(args.expected_plugin_sha256) is None:
        raise ValueError("candidate ref or expected Plugin digest is invalid")
    plugin_root = args.plugin_root.resolve(strict=True)
    application_root = args.application_root.resolve(strict=True)
    actual_plugin_sha256 = plugin_digest(plugin_root)
    if actual_plugin_sha256 != args.expected_plugin_sha256:
        raise ValueError("installed Plugin content differs from the candidate digest")

    manifest = json.loads(regular_bytes(plugin_root / ".codex-plugin" / "plugin.json", 128 * 1024))
    plugin_version = manifest.get("version")
    if not isinstance(plugin_version, str) or SEMVER.fullmatch(plugin_version) is None:
        raise ValueError("Plugin manifest version is invalid")

    scripts = plugin_root / "scripts"
    pins = powershell_pins(scripts / "bootstrap-pins.ps1") if args.platform == "windows-x64" else shell_pins(scripts / "bootstrap-pins.sh", args.platform)
    if GIT_SHA.fullmatch(pins["bootstrap_build_commit"]) is None or SHA256.fullmatch(pins["bootstrap_sha256"]) is None:
        raise ValueError("Bootstrap build commit or digest pin is invalid")
    launcher = scripts / ".bootstrap" / pins["bootstrap_filename"]
    launcher_sha256 = digest(regular_bytes(launcher, 50 * 1024 * 1024))
    if launcher_sha256 != pins["bootstrap_sha256"]:
        raise ValueError("installed Bootstrap launcher differs from its Plugin pin")

    pointer = json.loads(regular_bytes(application_root / "bin" / "current.json", 16 * 1024))
    if pointer.get("schemaVersion") != 1 or pointer.get("platform") != args.platform:
        raise ValueError("current Helper pointer schema or platform is invalid")
    helper_sha256 = pointer.get("sha256")
    helper_version_value = pointer.get("helperVersion")
    helper_filename = pointer.get("filename")
    if not isinstance(helper_sha256, str) or SHA256.fullmatch(helper_sha256) is None or not isinstance(helper_filename, str):
        raise ValueError("current Helper pointer identity is invalid")
    helper = application_root / "bin" / str(helper_version_value) / helper_filename
    if digest(regular_bytes(helper, 50 * 1024 * 1024)) != helper_sha256:
        raise ValueError("installed versioned Helper differs from current.json")
    recovery_name = "codex-skin.exe" if args.platform == "windows-x64" else "codex-skin"
    recovery_sha256 = digest(regular_bytes(application_root / "recovery" / "engine" / recovery_name, 50 * 1024 * 1024))
    if recovery_sha256 != helper_sha256:
        raise ValueError("recovery engine differs from the current versioned Helper")

    version = helper_version(helper)
    expected_tag = "helper-v" + str(helper_version_value)
    if (
        version.get("helperVersion") != helper_version_value
        or version.get("pluginVersion") != plugin_version
        or version.get("helperReleaseTag") != expected_tag
        or version.get("apiOrigin") != args.expected_api_origin
        or version.get("buildCommit") != args.candidate_ref
    ):
        raise ValueError("Helper version attestation does not match the installed closure")

    report = {
        "schemaVersion": 1,
        "candidateRef": args.candidate_ref,
        "pluginVersion": plugin_version,
        "pluginContentSHA256": actual_plugin_sha256,
        "bootstrapVersion": pins["bootstrap_version"],
        "bootstrapReleaseTag": pins["bootstrap_release_tag"],
        "bootstrapBuildCommit": pins["bootstrap_build_commit"],
        "bootstrapBuiltAt": pins["bootstrap_built_at"],
        "bootstrapSHA256": launcher_sha256,
        "helperVersion": helper_version_value,
        "helperReleaseTag": expected_tag,
        "helperBuildCommit": version["buildCommit"],
        "helperBuiltAt": version.get("builtAt"),
        "apiOrigin": version["apiOrigin"],
        "helperSHA256": helper_sha256,
        "recoverySHA256": recovery_sha256,
        "closureMatches": True,
    }
    print(json.dumps(report, sort_keys=True))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, subprocess.SubprocessError, json.JSONDecodeError) as error:
        print(f"Installation attestation failed: {error}", file=sys.stderr)
        raise SystemExit(1)
