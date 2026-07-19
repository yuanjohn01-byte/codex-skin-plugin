# Codex Skin Plugin

This is the standalone Public Plugin repository for Codex Skin. The installable Plugin root is `plugins/codex-skin/`; the repository root contains the Git-backed Marketplace metadata and release documentation.

Current status: the pre-release Marketplace exposes the Codex Skin v0.0.2 upgrade candidate from this repository. The distribution spike remains a read-only version check; no theme capability or public compatibility claim is attached.

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
  tools/
    validate_public_repo.py
    test_public_repository.py
```

The bundled v0.0.2 Skill is a read-only installation and upgrade check. Production theme Skills, Helper, contracts, keys and platform adapters are intentionally deferred to the numbered M1/M4 tasks in the Private project plan.

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
