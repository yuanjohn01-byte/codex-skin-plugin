# Codex Skin Plugin

This is the standalone Public Plugin repository for Codex Skin. The installable Plugin root is `plugins/codex-skin/`; the repository root contains the Git-backed Marketplace metadata and release documentation.

Current status: the pre-release Marketplace exposes the Codex Skin v0.0.2 upgrade candidate from this repository. The distribution spike remains a read-only version check; no theme capability or public compatibility claim is attached.

S3 now includes the source for a self-contained Go Helper prototype. Its `version` and runtime-only `doctor` commands emit the generated public JSON Lines v1 contract, but the Helper is not yet bootstrapped by the installed Plugin and does not perform theme, Codex, network, or recovery operations.

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
  internal/                    # Helper CLI/protocol/release verification
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
```

The canonical Helper protocol and release descriptor Schemas live in the Private repository allowlist and are generated into `contracts/`. Direct edits to a Public Schema or its digest manifest fail the repository boundary check.

The build test produces unsigned internal artifacts for `macos-arm64`, `macos-x64`, and `windows-x64` under ignored `dist/helper/`, validates Mach-O/PE architecture headers, and compares two clean builds byte-for-byte. Release assets are not committed to Git. Windows CI executes the native x64 Helper after removing Node, Python, and Go from `PATH`.

`tools/create_release_descriptor.py` converts that trusted build summary into one canonical, fixed-order descriptor with the exact version, tag, UTC timestamp, platform filenames, sizes, and SHA-256 values. The Go release package rejects noncanonical JSON, unknown fields or signing key IDs, invalid detached Ed25519 signatures, missing/duplicate/mismatched platforms, unsupported runtimes, and downloaded bytes with the wrong size or digest. Tests generate ephemeral signing keys at runtime; this repository contains no release private key or Production trust-root claim. The S3 artifact remains an unsigned internal review artifact until the later signing and release gates are complete.

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
