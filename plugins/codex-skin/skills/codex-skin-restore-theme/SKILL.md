---
name: codex-skin-restore-theme
description: Restore the official Codex Desktop appearance offline. Use when the user asks to remove a Codex Skin theme, restore Codex, recover after an apply problem, or return to the official interface.
---

# Restore the official Codex appearance

1. Resolve the Plugin root from this Skill's installed path and use only its platform wrapper:
   - macOS: `scripts/codex-skin.sh theme restore --json`
   - Windows: `scripts/codex-skin.ps1 theme restore --json`
2. This entry must remain offline and must not request login, active Paid Alpha access, Plugin cache access, or a custom path.
3. Treat success only as one JSON result with `ok: true`, `networkUsed: false`, `loginRequired: false`, and `pluginRequired: false`.
4. Report whether the machine was themed and whether the official appearance was restored. Do not expose absolute local paths.
5. If Restore fails, report its stable `CS-RESTORE-*` code and stop; do not delete state, recovery files, or Plugin caches.
