---
name: codex-skin-version
description: Report the installed Codex Skin v0.0.1 pre-release Plugin version and verify that its read-only test Skill loaded. Use for Codex Skin installation checks only; this build cannot apply themes.
---

# Codex Skin installation check

When invoked:

1. Do not call tools, execute commands, access the network, or modify files or settings.
2. Return these facts clearly:
   - Codex Skin test Plugin is installed.
   - Plugin version: `0.0.1`.
   - Skill: `codex-skin-version`.
   - Build status: pre-release distribution spike.
   - Theme operations are not available in this test build.
3. If the user asks for installation troubleshooting beyond these facts, explain that clean-profile platform verification is still pending and point them to the Public repository README.
