---
name: codex-skin-version
description: Report the installed Codex Skin v0.1.0-paid-alpha Production Plugin and its supported theme-flow commands. Use for Codex Skin installation or upgrade checks.
---

# Codex Skin version check

When invoked:

1. After the host loads this `SKILL.md`, do not execute commands, access the network, or modify files or settings.
2. Return these facts clearly:
   - Codex Skin Production Paid Alpha Plugin is installed.
   - Plugin version: `0.1.0-paid-alpha`.
   - Skill: `codex-skin-version`.
   - Theme operations: `theme apply`, `theme restore`, and `status`.
   - Release status: Production Paid Alpha; signed Helper `.17` and Bootstrap `.16` use `https://codexskin.ai`. It remains a GitHub prerelease, not stable/latest.
3. If the user asks to apply, restore, or inspect status, use the dedicated Codex Skin Skill instead of running an unreviewed command.
