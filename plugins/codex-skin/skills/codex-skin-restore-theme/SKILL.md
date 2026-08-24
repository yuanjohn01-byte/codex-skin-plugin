---
name: codex-skin-restore-theme
description: Restore the official Codex Desktop appearance offline. Use when the user asks to remove a Codex Skin theme, restore Codex, recover after an apply problem, or return to the official interface.
---

# Restore the official Codex appearance

1. Resolve the Plugin root from this Skill's installed path and use only its platform wrapper:
   - macOS: `scripts/codex-skin.sh theme restore --json`
   - Windows: `scripts/codex-skin.ps1 theme restore --json`
2. This entry must remain offline and must not request login, active Paid Alpha access, Plugin cache access, or a custom path. It runs one on-demand restore transaction, removes the skin, and exactly restores the native `system`/`light`/`dark` appearance (and saved related code-theme setting) that existed before the first Apply.
3. If Restore returns `CS-FLOW-RESTART-001` with action `confirm_restart`, explain that the current Codex window must close and reopen once, and ask for an explicit yes/no confirmation. Do not run the continuation command until the user says yes.
4. After explicit confirmation, run only the matching platform wrapper with `theme launch --json`. The one-time Helper owns the restart, restore, and visible verification; do not run a second launch. After Codex reopens, run `status --json`. Report success only when `restartKind` is `restore` and `restartStatus` is `completed`.
5. When no restart is required, treat success only as one JSON result with `ok: true`, `networkUsed: false`, `loginRequired: false`, and `pluginRequired: false`.
6. Report whether the machine was themed and whether the official appearance was restored. Do not expose absolute local paths.
7. If Restore or its restart continuation fails, report the stable code and stop; do not delete state, recovery files, or Plugin caches.
