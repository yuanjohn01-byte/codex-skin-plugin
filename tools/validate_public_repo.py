#!/usr/bin/env python3
"""Fail closed on files that can enter the Codex Skin Public repository."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path
from urllib.parse import urlparse


DEFAULT_ROOT = Path(__file__).resolve().parents[1]
ALLOWED_TOP_LEVEL = {
    ".agents",
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
MARKETPLACE_RELATIVE = Path(".agents/plugins/marketplace.json")
VERSION_SKILL_RELATIVE = PLUGIN_ROOT / "skills/codex-skin-version/SKILL.md"
README_RELATIVE = Path("README.md")
EXPORT_MANIFEST_RELATIVE = Path("contracts/export-manifest.json")
EXPORTED_CONTRACTS = (
    (
        Path("contracts/helper-protocol-v1.schema.json"),
        "codex-skin/contracts/public/helper-protocol-v1.schema.json",
    ),
    (
        Path("contracts/helper-release-descriptor-v1.schema.json"),
        "codex-skin/contracts/public/helper-release-descriptor-v1.schema.json",
    ),
    (
        Path("contracts/device-authorization-poll-v1.schema.json"),
        "codex-skin/contracts/public/device-authorization-poll-v1.schema.json",
    ),
)
EXPORTED_FIXTURES = (
    (
        Path("fixtures/free-test-theme-v1/fixture-policy-v1.json"),
        "codex-skin/fixtures/public/free-test-theme-v1/fixture-policy-v1.json",
    ),
    (
        Path("fixtures/free-test-theme-v1/fixture-provenance.json"),
        "codex-skin/fixtures/public/free-test-theme-v1/fixture-provenance.json",
    ),
    (
        Path("fixtures/free-test-theme-v1/manifest.json"),
        "codex-skin/fixtures/public/free-test-theme-v1/manifest.json",
    ),
    (
        Path("fixtures/free-test-theme-v1/assets/synthetic-dawn.png"),
        "codex-skin/fixtures/public/free-test-theme-v1/assets/synthetic-dawn.png",
    ),
)
EXPECTED_PLUGIN_VERSION = "0.0.2"
INSTALL_COMMANDS = (
    "codex plugin marketplace add yuanjohn01-byte/codex-skin-plugin --ref main",
    "codex plugin add codex-skin@codex-skin",
    "codex plugin list --json",
)
UPGRADE_COMMAND = "codex plugin marketplace upgrade codex-skin"
FALLBACK_COMMAND = "codex plugin marketplace remove codex-skin"
STRICT_SEMVER = re.compile(
    r"^(0|[1-9]\d*)\."
    r"(0|[1-9]\d*)\."
    r"(0|[1-9]\d*)"
    r"(?:-(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(?:\."
    r"(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*)?"
    r"(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$"
)
ALLOWED_MANIFEST_FIELDS = {
    "author",
    "description",
    "homepage",
    "interface",
    "keywords",
    "license",
    "name",
    "repository",
    "skills",
    "version",
}
ALLOWED_INTERFACE_FIELDS = {
    "brandColor",
    "capabilities",
    "category",
    "composerIcon",
    "defaultPrompt",
    "developerName",
    "displayName",
    "logo",
    "logoDark",
    "longDescription",
    "privacyPolicyURL",
    "screenshots",
    "shortDescription",
    "termsOfServiceURL",
    "websiteURL",
}


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


def absolute_https_url(value: object) -> bool:
    if not non_empty_string(value):
        return False
    parsed = urlparse(value)
    return parsed.scheme == "https" and bool(parsed.netloc)


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
    if not isinstance(version, str) or STRICT_SEMVER.fullmatch(version) is None:
        errors.append("plugin manifest version must be strict semver")
    elif version != EXPECTED_PLUGIN_VERSION:
        errors.append(f"plugin manifest version must be {EXPECTED_PLUGIN_VERSION}")
    if not non_empty_string(payload.get("description")):
        errors.append("plugin manifest description must be a non-empty string")

    for key in sorted(set(payload) - ALLOWED_MANIFEST_FIELDS):
        errors.append(f"plugin manifest field is not approved for the MVP: {key}")

    author = payload.get("author")
    if not isinstance(author, dict) or not non_empty_string(author.get("name")):
        errors.append("plugin manifest author.name must be a non-empty string")
    else:
        for key in sorted(set(author) - {"name", "email", "url"}):
            errors.append(f"plugin manifest author field is not supported: {key}")
        if not absolute_https_url(author.get("url")):
            errors.append("plugin manifest author.url must be an absolute HTTPS URL")

    if not absolute_https_url(payload.get("homepage")):
        errors.append("plugin manifest homepage must be an absolute HTTPS URL")
    keywords = payload.get("keywords")
    if (
        not isinstance(keywords, list)
        or not keywords
        or any(not non_empty_string(item) for item in keywords)
    ):
        errors.append("plugin manifest keywords must be a non-empty string array")

    interface = payload.get("interface")
    if not isinstance(interface, dict):
        errors.append("plugin manifest interface must be an object")
    else:
        for key in sorted(set(interface) - ALLOWED_INTERFACE_FIELDS):
            errors.append(f"plugin manifest interface field is not supported: {key}")
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
        prompts = interface.get("defaultPrompt")
        if isinstance(prompts, list) and (
            len(prompts) > 3
            or any(isinstance(item, str) and len(item) > 128 for item in prompts)
        ):
            errors.append("plugin manifest interface.defaultPrompt must contain at most 3 entries of 128 characters")
        if not absolute_https_url(interface.get("websiteURL")):
            errors.append("plugin manifest interface.websiteURL must be an absolute HTTPS URL")
        for key in ("privacyPolicyURL", "termsOfServiceURL"):
            if key in interface and not absolute_https_url(interface.get(key)):
                errors.append(f"plugin manifest interface.{key} must be an absolute HTTPS URL")

    if payload.get("license") != "MIT":
        errors.append("plugin manifest license must match the approved MIT license")
    if payload.get("repository") != "https://github.com/yuanjohn01-byte/codex-skin-plugin":
        errors.append("plugin manifest repository must point to the Public GitHub repository")

    validate_component_path(root, payload, "skills", errors)

    manifest_directory = MANIFEST_RELATIVE.parent
    for relative in candidates:
        if relative.parent == manifest_directory and relative != MANIFEST_RELATIVE:
            errors.append(f"only plugin.json belongs in .codex-plugin: {relative}")


def validate_marketplace(root: Path, candidates: set[Path], errors: list[str]) -> None:
    marketplace = root / MARKETPLACE_RELATIVE
    if MARKETPLACE_RELATIVE not in candidates or not marketplace.is_file():
        errors.append(f"missing {MARKETPLACE_RELATIVE}")
        return
    try:
        payload = json.loads(marketplace.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        errors.append(f"invalid marketplace metadata: {exc}")
        return
    if not isinstance(payload, dict):
        errors.append("marketplace root must be an object")
        return
    if set(payload) != {"name", "interface", "plugins"}:
        errors.append("marketplace root fields must be name, interface, and plugins")
    if payload.get("name") != "codex-skin":
        errors.append("marketplace name must be codex-skin")
    interface = payload.get("interface")
    if not isinstance(interface, dict) or interface.get("displayName") != "Codex Skin":
        errors.append("marketplace interface.displayName must be Codex Skin")
    elif set(interface) != {"displayName"}:
        errors.append("marketplace interface only supports displayName")

    plugins = payload.get("plugins")
    if not isinstance(plugins, list) or len(plugins) != 1 or not isinstance(plugins[0], dict):
        errors.append("marketplace must expose exactly one plugin entry")
        return
    entry = plugins[0]
    if set(entry) != {"name", "source", "policy", "category"}:
        errors.append("marketplace plugin fields must be name, source, policy, and category")
    if entry.get("name") != "codex-skin":
        errors.append("marketplace plugin name must be codex-skin")
    if entry.get("category") != "Developer Tools":
        errors.append("marketplace plugin category must be Developer Tools")

    source = entry.get("source")
    if not isinstance(source, dict) or set(source) != {"source", "path"}:
        errors.append("marketplace source must contain only source and path")
    elif source.get("source") != "local" or source.get("path") != "./plugins/codex-skin":
        errors.append("marketplace source must be local ./plugins/codex-skin")
    else:
        target = (root / source["path"]).resolve()
        try:
            target.relative_to(root.resolve())
        except ValueError:
            errors.append("marketplace source resolves outside the repository")
        if not target.is_dir():
            errors.append("marketplace source target is missing")

    policy = entry.get("policy")
    expected_policy = {"installation": "AVAILABLE", "authentication": "ON_INSTALL"}
    if policy != expected_policy:
        errors.append("marketplace policy must be AVAILABLE with ON_INSTALL authentication")

    marketplace_directory = MARKETPLACE_RELATIVE.parent
    for relative in candidates:
        if relative.parent == marketplace_directory and relative != MARKETPLACE_RELATIVE:
            errors.append(f"only marketplace.json belongs in .agents/plugins: {relative}")


def validate_version_skill(root: Path, candidates: set[Path], errors: list[str]) -> None:
    skill = root / VERSION_SKILL_RELATIVE
    if VERSION_SKILL_RELATIVE not in candidates or not skill.is_file():
        errors.append(f"missing {VERSION_SKILL_RELATIVE}")
        return
    try:
        content = skill.read_text(encoding="utf-8")
    except OSError as exc:
        errors.append(f"cannot read version Skill: {exc}")
        return
    expected_frontmatter = (
        "---\n"
        "name: codex-skin-version\n"
        "description: Report the installed Codex Skin v0.0.2 pre-release Plugin version "
        "and verify that its read-only test Skill loaded after installation or upgrade. Use "
        "for Codex Skin distribution checks only; this build cannot apply themes.\n"
        "---\n"
    )
    if not content.startswith(expected_frontmatter):
        errors.append("version Skill frontmatter must match the approved v0.0.2 contract")
    for marker in (
        "Plugin version: `0.0.2`.",
        "Skill: `codex-skin-version`.",
        "Upgrade target: replaces the v0.0.1 distribution-spike bundle.",
        "Theme operations are not available in this test build.",
        "After the host loads this `SKILL.md`, do not call any additional tools, execute "
        "commands, access the network, or modify files or settings.",
    ):
        if marker not in content:
            errors.append(f"version Skill is missing required marker: {marker}")

    skills_root = PLUGIN_ROOT / "skills"
    for relative in candidates:
        if (
            (root / relative).is_file()
            and relative.parts[: len(skills_root.parts)] == skills_root.parts
            and relative != VERSION_SKILL_RELATIVE
        ):
            errors.append(f"v0.0.2 may contain only the version check Skill: {relative}")


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


def validate_exported_contracts(root: Path, candidates: set[Path], errors: list[str]) -> None:
    exported_artifacts = (*EXPORTED_CONTRACTS, *EXPORTED_FIXTURES)
    required = {relative for relative, _ in exported_artifacts} | {EXPORT_MANIFEST_RELATIVE}
    missing = sorted(required - candidates, key=lambda item: item.as_posix())
    if missing:
        for relative in missing:
            errors.append(f"missing exported public contract file: {relative}")
        return

    try:
        manifest = json.loads((root / EXPORT_MANIFEST_RELATIVE).read_text(encoding="utf-8"))
        artifacts = {
            relative: (root / relative).read_bytes()
            for relative, _ in exported_artifacts
        }
        schemas = {
            relative: ((root / relative).read_bytes(), json.loads((root / relative).read_bytes()))
            for relative, _ in EXPORTED_CONTRACTS
        }
    except (OSError, json.JSONDecodeError) as exc:
        errors.append(f"invalid exported public contract: {exc}")
        return

    expected_manifest = {
        "schemaVersion": 1,
        "generatedFrom": "codex-skin/contracts/public/export-allowlist.json",
        "artifacts": [
            {
                "destination": relative.as_posix(),
                "sha256": hashlib.sha256(artifacts[relative]).hexdigest(),
                "source": source,
            }
            for relative, source in exported_artifacts
        ],
    }
    if manifest != expected_manifest:
        errors.append("public contract export manifest or SHA-256 does not match the generated schema")

    protocol_schema = schemas[EXPORTED_CONTRACTS[0][0]][1]
    if not isinstance(protocol_schema, dict):
        errors.append("Helper protocol schema root must be an object")
    else:
        definitions = protocol_schema.get("$defs")
        if protocol_schema.get("$schema") != "https://json-schema.org/draft/2020-12/schema":
            errors.append("Helper protocol must use JSON Schema draft 2020-12")
        if not isinstance(definitions, dict) or not {
            "progressEvent",
            "resultEvent",
            "error",
            "versionData",
            "doctorData",
        }.issubset(definitions):
            errors.append("Helper protocol schema is missing required v1 definitions")

    release_schema = schemas[EXPORTED_CONTRACTS[1][0]][1]
    if not isinstance(release_schema, dict):
        errors.append("Helper release descriptor schema root must be an object")
    else:
        required_fields = release_schema.get("required")
        if release_schema.get("$schema") != "https://json-schema.org/draft/2020-12/schema":
            errors.append("Helper release descriptor must use JSON Schema draft 2020-12")
        if not isinstance(required_fields, list) or not {
            "schemaVersion",
            "helperVersion",
            "releaseTag",
            "publishedAt",
            "signingKeyId",
            "artifacts",
        }.issubset(required_fields):
            errors.append("Helper release descriptor schema is missing required v1 fields")
        if release_schema.get("additionalProperties") is not False:
            errors.append("Helper release descriptor schema must reject unknown root fields")

    poll_schema = schemas[EXPORTED_CONTRACTS[2][0]][1]
    if not isinstance(poll_schema, dict):
        errors.append("Device authorization poll schema root must be an object")
    else:
        definitions = poll_schema.get("$defs")
        endpoints = poll_schema.get("x-endpoints")
        if poll_schema.get("$schema") != "https://json-schema.org/draft/2020-12/schema":
            errors.append("Device authorization poll contract must use JSON Schema draft 2020-12")
        if not isinstance(definitions, dict) or not {
            "proofRequest",
            "pollErrorEnvelope",
            "cancelSuccessEnvelope",
            "tokenSuccessEnvelope",
            "refreshRequest",
            "tokenErrorEnvelope",
            "logoutSuccessEnvelope",
            "logoutErrorEnvelope",
        }.issubset(definitions):
            errors.append("Device authorization poll contract is missing required v1 definitions")
        if endpoints != {
            "poll": "/api/v1/plugin/device-authorizations/token",
            "cancel": "/api/v1/plugin/device-authorizations/cancel",
            "refresh": "/api/v1/plugin/token/refresh",
            "logout": "/api/v1/plugin/logout",
        }:
            errors.append("Device authorization poll contract endpoint paths are invalid")

    for relative in candidates:
        if relative.parts and relative.parts[0] == "contracts" and relative not in required:
            errors.append(f"contract file is not in the generated Public allowlist: {relative}")


def validate_installation_instructions(
    root: Path, candidates: set[Path], errors: list[str]
) -> None:
    readme = root / README_RELATIVE
    if README_RELATIVE not in candidates or not readme.is_file():
        errors.append("missing Public README installation instructions")
        return
    try:
        content = readme.read_text(encoding="utf-8")
    except OSError as exc:
        errors.append(f"cannot read Public README: {exc}")
        return

    for command in (*INSTALL_COMMANDS, UPGRADE_COMMAND, FALLBACK_COMMAND):
        if command not in content:
            errors.append(f"README installation contract is missing command: {command}")
    for marker in (
        "exactly one installed `codex-skin@codex-skin` entry",
        "Completely quit Codex",
        "new task",
        "Do not edit Codex configuration or delete Marketplace/Plugin cache directories.",
        "leave it installed",
        "post-merge two-platform check",
    ):
        if marker not in content:
            errors.append(f"README installation contract is missing safety marker: {marker}")

    forbidden_commands = (
        "codex plugin list --available\n",
        "codex plugin marketplace add https://github.com/",
        "--ref codex/",
    )
    for command in forbidden_commands:
        if command in content:
            errors.append(f"README contains a non-canonical installation command: {command!r}")


def validate(root: Path) -> list[str]:
    errors: list[str] = []
    candidate_list, enumeration_error = repository_candidates(root)
    if enumeration_error:
        return [enumeration_error]
    candidates = set(candidate_list)
    validate_manifest(root, candidates, errors)
    validate_marketplace(root, candidates, errors)
    validate_version_skill(root, candidates, errors)
    validate_license(root, candidates, errors)
    validate_exported_contracts(root, candidates, errors)
    validate_installation_instructions(root, candidates, errors)
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
