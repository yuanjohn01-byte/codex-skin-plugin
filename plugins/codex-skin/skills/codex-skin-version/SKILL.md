---
name: codex-skin-version
description: Report the installed Codex Skin v0.1.0-paid-alpha Plugin candidate and its supported theme-flow commands. Use for Codex Skin installation or upgrade checks.
---

# Codex Skin version check

When invoked:

1. After the host loads this `SKILL.md`, do not execute commands, access the network, or modify files or settings.
2. Return these facts clearly:
   - Codex Skin Paid Alpha Plugin candidate is installed.
   - Plugin version: `0.1.0-paid-alpha`.
   - Skill: `codex-skin-version`.
   - Theme operations: `theme apply`, `theme restore`, and `status`.
   - Release status: code-stage candidate; not a Production release until the documented signing, Helper Release, API deployment, and dual-platform gates pass.
3. If the user asks to apply, restore, or inspect status, use the dedicated Codex Skin Skill instead of running an unreviewed command.
