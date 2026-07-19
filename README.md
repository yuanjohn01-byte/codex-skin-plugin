# Codex Skin Plugin

This is the standalone Public Plugin repository for Codex Skin. The installable Plugin root is `plugins/codex-skin/`; the repository root contains the Git-backed Marketplace metadata and release documentation.

Current status: the pre-release Marketplace exposes Codex Skin v0.0.1 from this repository. Local and Git snapshot ingestion are tested in isolated Codex CLI profiles; clean-profile macOS/Windows Desktop verification is still pending and no public compatibility claim is attached.

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

The bundled v0.0.1 Skill is a read-only installation check. Production theme Skills, Helper, contracts, keys and platform adapters are intentionally deferred to the numbered M1/M4 tasks in the Private project plan.

## License

Codex Skin Plugin is available under the [MIT License](LICENSE). Third-party components and assets remain subject to their own applicable licenses and notices.
