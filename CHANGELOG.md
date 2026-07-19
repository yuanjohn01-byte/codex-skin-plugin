# Changelog

## 0.0.2 - Unreleased

- Bump the read-only distribution spike from v0.0.1 to v0.0.2.
- Keep the Plugin identity and single installation-check Skill stable across the upgrade.
- Add v0.0.1-to-v0.0.2 clean-profile upgrade verification on macOS and Windows CLI environments.
- Add the guarded `main` installation/upgrade flow, JSON verification, restart/new-task check, and non-destructive failure fallback.
- Record successful v0.0.1-to-v0.0.2 Desktop/CLI upgrade checks on macOS and Windows feature refs.
- Enforce the canonical README commands and safety markers with positive and negative repository fixtures.
- Add the self-contained Go Helper source with minimal `version` and runtime-only `doctor` JSONL commands.
- Add the generated Helper protocol v1 Schema and Private-to-Public export digest gate.
- Add reproducible, CGO-free macOS arm64/x64 and Windows x64 Helper test builds.
- Run the Windows x64 Helper with Node, Python, and Go removed from `PATH`.
- Add the generated Helper release descriptor v1 Schema and deterministic unsigned descriptor generator.
- Verify canonical descriptor bytes, detached Ed25519 signatures, strict SemVer/platform selection, and artifact size/SHA-256 before use.
- Add an out-of-Plugin-cache per-user bootstrap with restricted GitHub Release downloads, staged self-test, version directories, and atomic `current.json` activation.
- Preserve the previous Helper, state, and recovery files across failed bootstrap, Plugin cache replacement, and successful upgrade tests.
- Add a secret-free macOS ad-hoc signing/tamper feasibility gate and document the remaining Developer ID, notarization, Gatekeeper, and stapled-container limits.
- Add an ephemeral Windows self-signed Authenticode/tamper/cleanup gate and document the remaining public trust, timestamp, and SmartScreen reputation limits.
- Reject descriptor/signature tampering, artifact mismatch, HTTP truncation, interrupted reads, and downgrade before activation while preserving a reusable last-known-good Helper.

No theme operation or public compatibility claim is attached to this version.

## 0.0.1 - Unreleased

- Create the local Public Plugin repository boundary.
- Add a validation-ready `codex-skin` Plugin manifest.
- Add the single-entry Git-backed Marketplace catalog and strict metadata validation.
- Add the read-only `codex-skin-version` installation check Skill.
- Add a clean Windows runner smoke test for Marketplace/install/cache integrity.
- Add repository hygiene and public-boundary checks.
- Adopt the MIT license and enforce a minimal Public tracked-file allowlist.

No public release or compatibility claim is attached to this version.
