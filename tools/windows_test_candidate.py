#!/usr/bin/env python3
"""Build an opt-in candidate without changing installed/default Plugin pins.

This command never signs, uploads, installs, or changes a Codex profile. Its ZIP is
usable only after the matching immutable signed Release assets are available.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
import zipfile
from pathlib import Path

from create_release_descriptor import canonical_bytes, descriptor_from_summary
from release_profiles import WINDOWS_TEST
from render_bootstrap_pins import load as load_bootstrap


ROOT = Path(__file__).resolve().parents[1]
PROFILE = WINDOWS_TEST
BRANCH = "codex/win-002-test-channel"
SIGNER_COMMIT = "45f16ea2f63d4d60419dab65ede1f87b1471f80f"


def run(*args: str) -> str:
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()


def python_tool(name: str, *args: str) -> None:
    subprocess.run([sys.executable, f"tools/{name}.py", *args], cwd=ROOT, check=True)


def freeze(expected_sha: str) -> tuple[str, str]:
    if not re.fullmatch(r"[0-9a-f]{40}", expected_sha):
        raise ValueError("an exact 40-character candidate SHA is required")
    if run("git", "rev-parse", "HEAD") != expected_sha:
        raise ValueError("checkout does not match the requested candidate")
    if run("git", "status", "--porcelain", "--untracked-files=all"):
        raise ValueError("commit the candidate before building; worktree must be clean")
    tags = run("git", "tag", "--list", PROFILE.helper_release_tag)
    if tags:
        raise ValueError("test version already exists; choose a new immutable test version")
    return expected_sha, run("git", "show", "-s", "--format=%cI", expected_sha)


def verify_payload(root: Path, sha: str) -> dict[str, object]:
    helper = json.loads((root / "helper/build-summary.json").read_text())
    expected = descriptor_from_summary(helper, PROFILE.signing_key_id, PROFILE.name)
    if (root / "helper/helper-release-descriptor.json").read_bytes() != canonical_bytes(expected):
        raise ValueError("descriptor differs from the fixed-profile build summary")
    bootstrap = load_bootstrap(root / "bootstrap/build-summary.json", PROFILE.name)
    timestamps = set()
    for directory, items in (("helper", helper["artifacts"]), ("bootstrap", bootstrap.values())):
        for item in items:
            if item["buildCommit"] != sha:
                raise ValueError("artifact provenance differs from candidate SHA")
            timestamps.add(item["builtAt"])
            path = root / directory / item["filename"]
            if path.is_symlink() or not path.is_file():
                raise ValueError("artifact must be a regular file")
            data = path.read_bytes()
            if len(data) != item["size"] or hashlib.sha256(data).hexdigest() != item["sha256"]:
                raise ValueError("artifact hash/size mismatch")
    if len(timestamps) != 1:
        raise ValueError("artifact build timestamps differ")
    return expected


def package_plugin(root: Path, sha: str) -> str:
    # Read only committed, public Plugin files. Never copy a worktree directory,
    # ignored files, private handoffs, local caches, or an installed marketplace.
    paths = run(
        "git", "ls-tree", "-r", "--name-only", sha, "--",
        "plugins/codex-skin", ".agents/plugins/marketplace.json", "LICENSE", "NOTICE",
    ).splitlines()
    package = root / "marketplace"
    package.mkdir()
    for relative in paths:
        mode = run("git", "ls-tree", sha, "--", relative).split()[0]
        if mode not in {"100644", "100755"}:
            raise ValueError("Plugin package may contain only committed regular files")
        destination = package / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_bytes(subprocess.check_output(["git", "show", f"{sha}:{relative}"], cwd=ROOT))
    manifest_path = package / "plugins/codex-skin/.codex-plugin/plugin.json"
    manifest = json.loads(manifest_path.read_text())
    if manifest["name"] != "codex-skin":
        raise ValueError("unexpected Plugin identity")
    manifest["version"] = manifest["version"].split("+")[0] + f"+codex.{sha}"
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n")
    version_skill = package / "plugins/codex-skin/skills/codex-skin-version/SKILL.md"
    version_skill.write_text(f"""---
name: codex-skin-version
description: Report this opt-in Windows test Plugin package identity for installation and upgrade checks.
---

# Codex Skin Windows test package

When invoked, report these package facts without executing commands or changing settings:

- Channel: opt-in Windows test, not the default Production Plugin.
- Plugin package version: `{manifest['version']}`.
- Candidate source SHA: `{sha}`.
- Packaged Helper target: `{PROFILE.helper_version}`.
- Packaged Bootstrap target: `{PROFILE.bootstrap_version}`.
- API origin: `{PROFILE.api_base_url}`.
- Operations: `theme apply`, `theme restore`, and `status`.

These are the loaded package's identity and download targets, not proof that the Helper
is installed, upgraded, or that a theme has been applied. For current local runtime or
theme state, use the dedicated `codex-skin-status` skill when requested. Apply and Restore
continue to use their dedicated skills and normal confirmation rules.
""", encoding="utf-8")
    python_tool(
        "render_bootstrap_pins", "--summary", str(root / "bootstrap/build-summary.json"),
        "--release-profile", PROFILE.name,
        "--shell-output", str(package / "plugins/codex-skin/scripts/bootstrap-pins.sh"),
        "--powershell-output", str(package / "plugins/codex-skin/scripts/bootstrap-pins.ps1"),
    )
    with zipfile.ZipFile(root / "windows-test-marketplace.zip", "x", zipfile.ZIP_DEFLATED) as archive:
        for path in sorted(package.rglob("*")):
            if path.is_file():
                info = zipfile.ZipInfo(path.relative_to(package).as_posix(), (2026, 1, 1, 0, 0, 0))
                info.external_attr = 0o100644 << 16
                info.compress_type = zipfile.ZIP_DEFLATED
                archive.writestr(info, path.read_bytes())
    return manifest["version"]


def build(root: Path, sha: str) -> None:
    sha, timestamp = freeze(sha)
    # Refuse reused output so a failed build cannot mix with an older candidate.
    root.mkdir(parents=True, exist_ok=False)
    for tool, directory in (("build_helper", "helper"), ("build_bootstrap", "bootstrap")):
        python_tool(tool, "--commit", sha, "--built-at", timestamp,
                    "--release-profile", PROFILE.name, "--output", str(root / directory))
    python_tool("create_sbom", "--summary", str(root / "helper/build-summary.json"),
                "--output", str(root / "helper/sbom.spdx.json"), "--release-profile", PROFILE.name)
    python_tool("create_release_descriptor", "--summary", str(root / "helper/build-summary.json"),
                "--output", str(root / "helper/helper-release-descriptor.json"),
                "--key-id", PROFILE.signing_key_id, "--release-profile", PROFILE.name)
    descriptor = verify_payload(root, sha)
    plugin_version = package_plugin(root, sha)
    freeze(sha)
    manifest = {
        "schemaVersion": 1, "channel": PROFILE.name, "candidateSHA": sha,
        "helperVersion": PROFILE.helper_version, "bootstrapVersion": PROFILE.bootstrap_version,
        "pluginVersion": plugin_version, "releaseTag": PROFILE.helper_release_tag,
        "apiOrigin": PROFILE.api_base_url, "signerCommit": SIGNER_COMMIT,
        "descriptorSHA256": hashlib.sha256(canonical_bytes(descriptor)).hexdigest(),
        "marketplaceSHA256": hashlib.sha256((root / "windows-test-marketplace.zip").read_bytes()).hexdigest(),
        "requiresSignedPublishedAssets": True,
    }
    (root / "candidate.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(json.dumps(manifest, indent=2))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--candidate-sha", required=True)
    parser.add_argument("--output", type=Path, default=ROOT / "dist/windows-test")
    args = parser.parse_args()
    build(args.output.resolve(), args.candidate_sha)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, subprocess.CalledProcessError) as exc:
        print(f"Windows test candidate failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
