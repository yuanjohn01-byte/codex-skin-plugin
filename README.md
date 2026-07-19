# Codex Skin Plugin

This is the standalone Public Plugin repository for Codex Skin. The installable Plugin root is `plugins/codex-skin/`; the repository root contains the Git-backed Marketplace metadata and release documentation.

Current status: the pre-release Marketplace exposes the Codex Skin v0.0.2 upgrade candidate from this repository. The distribution spike remains a read-only version check; no theme capability or public compatibility claim is attached.

The macOS upgrade spike starts from the v0.0.1 Git ref and checks replacement by v0.0.2 in an isolated Codex CLI profile. The Windows distribution workflow performs the equivalent CLI/cache upgrade on a clean GitHub-hosted runner. Windows Store Codex Desktop UI, restart, and authenticated new-task coverage remain a separate real-machine gate.

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

## License

Codex Skin Plugin is available under the [MIT License](LICENSE). Third-party components and assets remain subject to their own applicable licenses and notices.
