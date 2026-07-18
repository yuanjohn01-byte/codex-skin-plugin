#!/usr/bin/env python3
"""Fail closed on files that can enter the Codex Skin Public repository."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path


DEFAULT_ROOT = Path(__file__).resolve().parents[1]
ALLOWED_TOP_LEVEL = {
    ".editorconfig",
    ".gitattributes",
    ".github",
    ".gitignore",
    ".prettierignore",
    ".prettierrc",
    ".prettierrc.json",
    "AGENTS.md",
    "CHANGELOG.md",
    "Cargo.lock",
    "Cargo.toml",
    "LICENSE",
    "Makefile",
    "NOTICE",
    "README.md",
    "SECURITY.md",
    "cmd",
    "components.json",
    "contracts",
    "crates",
    "docs",
    "eslint.config.mjs",
    "fixtures",
    "go.mod",
    "go.sum",
    "internal",
    "justfile",
    "keys",
    "package.json",
    "pkg",
    "playwright.config.ts",
    "plugins",
    "pnpm-lock.yaml",
    "scripts",
    "src",
    "tests",
    "tools",
    "tsconfig.json",
    "vitest.config.ts",
}
FORBIDDEN_PREFIXES = {
    ("contracts", "private"),
    ("docs", "archive"),
    ("docs", "evidence"),
    ("docs", "handoff"),
    ("docs", "internal"),
    ("docs", "planning"),
}
FORBIDDEN_COMPONENTS = {
    ".claude",
    ".codex",
    ".codex-skin-local",
    ".history",
    ".idea",
    ".vscode",
    "customer-data",
    "license-proof",
    "personal",
    "private",
    "prompts",
    "source-art",
    "source_art",
    "themes",
    "transcripts",
    "user-data",
}
FORBIDDEN_SUFFIXES = {
    ".cskin",
    ".db",
    ".dmg",
    ".dump",
    ".exe",
    ".key",
    ".msi",
    ".p12",
    ".pem",
    ".pfx",
    ".pkg",
    ".sqlite",
    ".sqlite3",
    ".tar",
    ".tgz",
    ".zip",
}
TEXT_SUFFIXES = {
    "",
    ".css",
    ".go",
    ".html",
    ".js",
    ".json",
    ".jsx",
    ".md",
    ".mjs",
    ".ps1",
    ".py",
    ".rs",
    ".sh",
    ".toml",
    ".ts",
    ".tsx",
    ".txt",
    ".yaml",
    ".yml",
}
MAX_FILE_BYTES = 5 * 1024 * 1024
SECRET_PATTERNS = {
    "private-key": re.compile(r"BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY"),
    "aws-access-key": re.compile(r"AKIA[0-9A-Z]{16}"),
    "github-token": re.compile(r"(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{60,})"),
    "generic-secret-assignment": re.compile(
        r"(?i)(?:api[_-]?key|client[_-]?secret|access[_-]?token|refresh[_-]?token|cookie)"
        r"\s*[:=]\s*['\"][^'\"]{12,}"
    ),
}
SELF = Path("tools/validate_public_repo.py")
PLUGIN_ROOT = Path("plugins/codex-skin")
MANIFEST_RELATIVE = PLUGIN_ROOT / ".codex-plugin/plugin.json"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=DEFAULT_ROOT)
    return parser.parse_args()


def repository_candidates(root: Path) -> tuple[list[Path], str | None]:
    result = subprocess.run(
        ["git", "-C", str(root), "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
        check=False,
        capture_output=True,
    )
    if result.returncode != 0:
        message = result.stderr.decode("utf-8", errors="replace").strip()
        return [], f"cannot enumerate repository files: {message}"
    paths = [Path(item.decode("utf-8")) for item in result.stdout.split(b"\0") if item]
    return sorted(set(paths), key=lambda item: item.as_posix()), None


def forbidden_path_reason(relative: Path) -> str | None:
    parts = tuple(part.lower() for part in relative.parts)
    name = relative.name.lower()
    if name == ".env" or name.startswith(".env.") or name == ".dev.vars" or name.startswith(".dev.vars."):
        return "environment file"
    if relative.parts and relative.parts[0] not in ALLOWED_TOP_LEVEL:
        return "top-level path outside the Public allowlist"
    if any(parts[: len(prefix)] == prefix for prefix in FORBIDDEN_PREFIXES):
        return "Private or local-only documentation path"
    if any(part in FORBIDDEN_COMPONENTS for part in parts):
        return "Private or local-only path component"
    if name.endswith((".local.md", ".notes.md", ".draft.md")):
        return "personal note or draft"
    if relative.suffix.lower() in FORBIDDEN_SUFFIXES:
        return "secret, database, binary, or generated package file"
    return None


def non_empty_string(value: object) -> bool:
    return isinstance(value, str) and bool(value.strip())


def validate_component_path(
    root: Path, payload: dict[str, object], key: str, errors: list[str]
) -> None:
    value = payload.get(key)
    if not isinstance(value, str) or not value.startswith("./"):
        errors.append(f"plugin manifest {key} must be a ./-prefixed path")
        return
    relative = Path(value)
    if relative.is_absolute() or ".." in relative.parts:
        errors.append(f"plugin manifest {key} must stay inside the plugin root")
        return
    plugin_root = (root / PLUGIN_ROOT).resolve()
    target = (plugin_root / relative).resolve()
    try:
        target.relative_to(plugin_root)
    except ValueError:
        errors.append(f"plugin manifest {key} resolves outside the plugin root")
        return
    if not target.is_dir():
        errors.append(f"plugin manifest {key} target is missing: {value}")


def validate_manifest(root: Path, candidates: set[Path], errors: list[str]) -> None:
    manifest = root / MANIFEST_RELATIVE
    if MANIFEST_RELATIVE not in candidates or not manifest.is_file():
        errors.append(f"missing {MANIFEST_RELATIVE}")
        return
    try:
        payload = json.loads(manifest.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        errors.append(f"invalid plugin manifest: {exc}")
        return

    if not isinstance(payload, dict):
        errors.append("plugin manifest root must be an object")
        return
    if payload.get("name") != "codex-skin":
        errors.append("plugin manifest name must be codex-skin")
    elif re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", payload["name"]) is None:
        errors.append("plugin manifest name must use kebab-case")
    version = payload.get("version")
    if not isinstance(version, str) or re.fullmatch(r"\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?", version) is None:
        errors.append("plugin manifest version must be semver")
    if not non_empty_string(payload.get("description")):
        errors.append("plugin manifest description must be a non-empty string")

    author = payload.get("author")
    if not isinstance(author, dict) or not non_empty_string(author.get("name")):
        errors.append("plugin manifest author.name must be a non-empty string")

    interface = payload.get("interface")
    if not isinstance(interface, dict):
        errors.append("plugin manifest interface must be an object")
    else:
        for key in (
            "displayName",
            "shortDescription",
            "longDescription",
            "developerName",
            "category",
        ):
            if not non_empty_string(interface.get(key)):
                errors.append(f"plugin manifest interface.{key} must be a non-empty string")
        for key in ("capabilities", "defaultPrompt"):
            value = interface.get(key)
            if value is not None and (
                not isinstance(value, list)
                or not value
                or any(not non_empty_string(item) for item in value)
            ):
                errors.append(f"plugin manifest interface.{key} must be a non-empty string array")

    if payload.get("license") != "MIT":
        errors.append("plugin manifest license must match the approved MIT license")
    if payload.get("repository") != "https://github.com/yuanjohn01-byte/codex-skin-plugin":
        errors.append("plugin manifest repository must point to the Public GitHub repository")

    validate_component_path(root, payload, "skills", errors)

    manifest_directory = MANIFEST_RELATIVE.parent
    for relative in candidates:
        if relative.parent == manifest_directory and relative != MANIFEST_RELATIVE:
            errors.append(f"only plugin.json belongs in .codex-plugin: {relative}")


def validate_license(root: Path, candidates: set[Path], errors: list[str]) -> None:
    license_relative = Path("LICENSE")
    license_path = root / license_relative
    if license_relative not in candidates or not license_path.is_file():
        errors.append("missing Public LICENSE")
        return
    try:
        content = license_path.read_text(encoding="utf-8")
    except OSError as exc:
        errors.append(f"cannot read Public LICENSE: {exc}")
        return
    for marker in ("MIT License", "Permission is hereby granted", "THE SOFTWARE IS PROVIDED \"AS IS\""):
        if marker not in content:
            errors.append(f"Public LICENSE is not the approved MIT text (missing {marker!r})")


def validate(root: Path) -> list[str]:
    errors: list[str] = []
    candidate_list, enumeration_error = repository_candidates(root)
    if enumeration_error:
        return [enumeration_error]
    candidates = set(candidate_list)
    validate_manifest(root, candidates, errors)
    validate_license(root, candidates, errors)
    proprietary_marker = "ship" + "any"

    for relative in candidate_list:
        path = root / relative
        reason = forbidden_path_reason(relative)
        if reason:
            errors.append(f"forbidden {reason}: {relative}")
            continue
        if not path.is_file():
            continue
        if path.is_symlink():
            errors.append(f"symbolic links are not allowed in Public source: {relative}")
            continue
        if path.stat().st_size > MAX_FILE_BYTES:
            errors.append(f"file exceeds 5 MiB Public source limit: {relative}")
        if relative == SELF or path.suffix.lower() not in TEXT_SUFFIXES:
            continue
        try:
            content = path.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError):
            continue
        if proprietary_marker in content.lower():
            errors.append(f"Private template marker found: {relative}")
        for label, pattern in SECRET_PATTERNS.items():
            if pattern.search(content):
                errors.append(f"possible {label}: {relative}")

    return sorted(set(errors))


def main() -> int:
    root = parse_args().root.resolve()
    errors = validate(root)
    if errors:
        print("Public repository validation failed:")
        for error in errors:
            print(f"- {error}")
        return 1
    print("Public repository boundary validation passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
