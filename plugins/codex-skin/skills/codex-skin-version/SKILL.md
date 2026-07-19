---
name: codex-skin-version
description: Report the installed Codex Skin v0.0.2 pre-release Plugin version and verify that its read-only test Skill loaded after installation or upgrade. Use for Codex Skin distribution checks only; this build cannot apply themes.
---

# Codex Skin installation check

When invoked:

1. After the host loads this `SKILL.md`, do not call any additional tools, execute commands, access the network, or modify files or settings.
2. Return these facts clearly:
   - Codex Skin test Plugin is installed.
   - Plugin version: `0.0.2`.
   - Skill: `codex-skin-version`.
   - Build status: pre-release distribution spike.
   - Upgrade target: replaces the v0.0.1 distribution-spike bundle.
   - Theme operations are not available in this test build.
3. If the user asks for installation troubleshooting beyond these facts, explain that clean-profile platform verification is still pending and point them to the Public repository README.
