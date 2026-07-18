# Codex Skin Plugin

This is the standalone Public Plugin repository for Codex Skin. The installable Plugin root is `plugins/codex-skin/`; the repository root will later contain Git-backed Marketplace and release metadata.

Current status: the minimal Public repository baseline is published at [yuanjohn01-byte/codex-skin-plugin](https://github.com/yuanjohn01-byte/codex-skin-plugin). It has not yet been installed into a Marketplace or verified on macOS/Windows Codex Desktop.

The repository uses the MIT license. Its tracked-file allowlist, secret/Private-path checks and negative fixtures must pass before every remote push. Founder approval to create the Public repository has been recorded.

## Current structure

```text
codex-skin-plugin/
  AGENTS.md
  LICENSE
  plugins/
    codex-skin/
      .codex-plugin/plugin.json
      skills/
      scripts/
      assets/
  tools/
    validate_public_repo.py
    test_public_repository.py
```

The Marketplace entry, production Skills, Helper, contracts, keys and platform adapters are intentionally deferred to the numbered M1/M4 tasks in the Private project plan.

## License

Codex Skin Plugin is available under the [MIT License](LICENSE). Third-party components and assets remain subject to their own applicable licenses and notices.
