# Codex Skin Plugin

This is the standalone Public Plugin repository for Codex Skin. The installable Plugin root is `plugins/codex-skin/`; the repository root contains the Git-backed Marketplace metadata and release documentation.

Current status: the pre-release Marketplace exposes the Codex Skin v0.0.2 upgrade candidate from this repository. The distribution spike remains a read-only version check; no theme capability or public compatibility claim is attached.

S3 now includes the source for a self-contained Go Helper prototype. Its `version` and runtime-only `doctor` commands emit the generated public JSON Lines v1 contract. The internal bootstrap can verify and stage a Helper outside the Plugin cache, but it is not yet exposed by the installed Plugin and does not perform theme, Codex, account, or recovery operations.

The v0.0.1-to-v0.0.2 upgrade spike has passed macOS and Windows Desktop/CLI checks against the reviewed feature refs. The Windows distribution workflow also performs the equivalent CLI/cache upgrade on a clean GitHub-hosted runner. Every release still requires a post-merge two-platform check of the exact `main` form before its commands are published.

The repository uses the MIT license. Its tracked-file allowlist, secret/Private-path checks and negative fixtures must pass before every remote push. Founder approval to create the Public repository has been recorded.

## Current structure

```text
codex-skin-plugin/
  AGENTS.md
  LICENSE
  .agents/
    plugins/
      marketplace.json
  plugins/
    codex-skin/
      .codex-plugin/plugin.json
      skills/
        codex-skin-version/SKILL.md
      scripts/
      assets/
  cmd/codex-skin/              # self-contained Helper entrypoint
  cmd/codex-skin-guardian/     # fixed-surface internal Guardian spike
  internal/                    # Helper and Guardian runtime/lifecycle packages
  contracts/                   # generated public contracts only
  tools/
    validate_public_repo.py
    test_public_repository.py
```

The bundled v0.0.2 Skill is a read-only installation and upgrade check. The S3 Helper source is not exposed as an installed capability yet. Production theme Skills, keys and platform adapters remain deferred to the numbered M1/M4 tasks in the Private project plan.

## Helper development

Go 1.26.5 is pinned in `go.mod`. The minimal contract checks are:

```bash
go test ./...
go vet ./...
go run ./cmd/codex-skin version --json
go run ./cmd/codex-skin doctor --json
python3 tools/test_helper_builds.py
python3 tools/test_release_descriptor.py
python3 tools/test_guardian_builds.py
```

The canonical Helper protocol and release descriptor Schemas live in the Private repository allowlist and are generated into `contracts/`. Direct edits to a Public Schema or its digest manifest fail the repository boundary check.

The build test produces unsigned internal artifacts for `macos-arm64`, `macos-x64`, and `windows-x64` under ignored `dist/helper/`, validates Mach-O/PE architecture headers, and compares two clean builds byte-for-byte. Release assets are not committed to Git. Windows CI executes the native x64 Helper after removing Node, Python, and Go from `PATH`.

`tools/create_release_descriptor.py` converts that trusted build summary into one canonical, fixed-order descriptor with the exact version, tag, UTC timestamp, platform filenames, sizes, and SHA-256 values. The Go release package rejects noncanonical JSON, unknown fields or signing key IDs, invalid detached Ed25519 signatures, missing/duplicate/mismatched platforms, unsupported runtimes, and downloaded bytes with the wrong size or digest. Tests generate ephemeral signing keys at runtime; this repository contains no release private key or Production trust-root claim. The S3 artifact remains an unsigned internal review artifact until the later signing and release gates are complete.

The bootstrap library uses the fixed Public GitHub Releases origin, accepts only the descriptor, raw detached signature, and strict Helper filenames, and allows HTTPS redirects only to GitHub release-asset hosts. After signature/platform/size/SHA-256 verification it writes a per-version executable in `~/Library/Application Support/CodexSkin/bin/` on macOS or `%LOCALAPPDATA%\CodexSkin\bin\` on Windows, runs only the fixed `version --json` and `doctor --json` self-tests with a minimal environment, then atomically replaces `current.json`. Descriptor/signature tampering, wrong artifact bytes, declared-length truncation, reader interruption, downgrade, and self-test failure all stop before activation; untrusted candidates never reach the executable self-test, and the previous pointer and Helper remain reusable without staging debris. The application root must not overlap or resolve through the Plugin cache; tests replace that cache and confirm the Helper plus `state/` and `recovery/` sentinels remain. This is still internal bootstrap infrastructure: no unsigned artifact is authorized for user installation.

The [macOS signing feasibility note](docs/macos-signing-feasibility.md) and its CI workflow test ad-hoc signing, strict verification, and post-signing tamper rejection without using secrets. Ad-hoc signatures are explicitly not Developer ID signatures or notarization; formal macOS distribution remains blocked on a protected Apple certificate, accepted notarization, the exact Gatekeeper download path, and a decision about a staplable release container.

The [Windows signing feasibility note](docs/windows-signing-feasibility.md) uses a one-run, non-exportable self-signed certificate only inside the current-user CI stores to test Authenticode signing, local-policy verification, signed Helper execution, PE tamper rejection, and certificate cleanup. It uploads only a non-secret JSON summary. Self-signing does not provide public trust or SmartScreen reputation; formal Windows distribution remains blocked on a protected public code-signing identity, RFC 3161 timestamp, and clean-machine testing of the exact final download channel.

The [per-user Guardian lifecycle note](docs/guardian-lifecycle-feasibility.md) describes the internal fixed-surface Guardian and its versioned install, signature gate, per-user registration, side-by-side upgrade, explicit rollback, and registration-first uninstall tests. Native macOS LaunchAgent and Windows Limited/Interactive Scheduled Task jobs create, run, inspect, and remove temporary registrations without adding a service, network listener, or general command surface. The trigger remains a packaging-only Spike; actual lifecycle reconciliation is a later numbered task, and formal Guardian distribution remains blocked on the same platform signing gates.

## Installation

The following is the single installation flow for releases on `main`. A release is ready for website publication only after its documented gates pass. Users do not need to open or fill in the Marketplace form, edit Codex configuration, or delete cache files.

Run these commands in a terminal:

```bash
codex plugin marketplace add yuanjohn01-byte/codex-skin-plugin --ref main
codex plugin add codex-skin@codex-skin
codex plugin list --json
```

The final command must show exactly one installed `codex-skin@codex-skin` entry with `installed: true` and `enabled: true`. Completely quit Codex, reopen it, start a new task, and ask Codex to run `$codex-skin-version`. A successful v0.0.2 distribution check reports Version `0.0.2`, Skill `codex-skin-version`, and that theme operations are unavailable in this test build.

The command shape has passed macOS and Windows Desktop/CLI tests against the reviewed feature refs. For every release, publishing the `main` form also requires a post-merge two-platform check.

## Upgrade

Refresh the existing Git-backed Marketplace snapshot, reinstall the same Plugin ID, and verify the result:

```bash
codex plugin marketplace upgrade codex-skin
codex plugin add codex-skin@codex-skin
codex plugin list --json
```

Then completely quit Codex, reopen it, and run `$codex-skin-version` in a new task. A stale version subtitle in the Plugin details view is not enough to diagnose a failed upgrade: compare the JSON result and the new-task Skill result first.

## Failure fallback

If Marketplace add/upgrade or Plugin add fails, stop before making manual changes. Keep an already installed Plugin in place, and save the failing command plus its original error. Collect only these shareable diagnostics:

```bash
codex --version
codex plugin marketplace list
codex plugin list --json
```

Do not share tokens, cookies, account data, prompts, source code, or absolute user paths. Do not edit Codex configuration or delete Marketplace/Plugin cache directories.

If the `codex-skin` Marketplace snapshot alone is missing or stale, refresh it with the reversible fallback below; this does not remove an installed Plugin:

```bash
codex plugin marketplace remove codex-skin
codex plugin marketplace add yuanjohn01-byte/codex-skin-plugin --ref main
codex plugin add codex-skin@codex-skin
codex plugin list --json
```

If an existing Plugin still works but the upgrade does not, leave it installed and report the diagnostics instead of uninstalling it. The v0.0.2 spike has no theme operations; production removal and official-appearance restoration will be documented with the later out-of-cache recovery implementation.

## License

Codex Skin Plugin is available under the [MIT License](LICENSE). Third-party components and assets remain subject to their own applicable licenses and notices.
