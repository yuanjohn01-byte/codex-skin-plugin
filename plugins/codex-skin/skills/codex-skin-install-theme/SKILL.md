---
name: codex-skin-install-theme
description: Apply a published Codex Skin theme by its six-digit theme ID. Use when the user asks to install, apply, try, or switch to a Codex Skin theme.
---

# Apply a Codex Skin theme

1. Obtain exactly one six-digit theme ID from the user or the selected Codex Skin catalog item. Never invent an ID.
2. Resolve the Plugin root from this Skill's installed path, then use only the platform wrapper inside `scripts/`:
   - macOS: `scripts/codex-skin.sh theme apply THEME_ID --json`
   - Windows: `scripts/codex-skin.ps1 theme apply THEME_ID --json`
3. Do not call Codex Skin HTTP endpoints directly and do not accept a custom API origin, download URL, package path, selector, CSS, JavaScript, or shell fragment.
4. Allow the command to stay active while it opens the same-origin authorization page and, for a Pro theme without access, the same-origin Pricing page. The command resumes automatically; do not ask the user to repeat the theme request.
5. If the command returns `CS-FLOW-RESTART-001` with action `confirm_restart`, explain that the current Codex window must close and reopen once so the verified Helper can use this same Codex profile. Ask for an explicit yes/no confirmation. Do not run the continuation command until the user says yes.
6. After explicit confirmation, run only the matching platform wrapper with `theme continue --json`. Treat `restartAccepted: true` only as acceptance of the restart, never as proof that the theme is already applied. Do not repeat authorization, purchase, or download.
7. After Codex reopens, run the platform wrapper with `status --json`. If `restartStatus` is `restart_approved` or `running`, wait briefly and re-check for no longer than 60 seconds; do not start another continuation. Treat apply as successful only when `restartStatus` is `completed` and `appliedThemePublicId`/`appliedThemeVersion` match the request. If restart status is `failed`, report only its stable `restartErrorCode`.
8. When no restart is required, treat success only as one JSON result with `ok: true`, the requested `themePublicId`, a `themeVersion`, and an `operationId`.
9. On `CS-FLOW-AUTH-001`, ask the user to finish or retry authorization. On `CS-FLOW-ACCESS-001`, explain that purchase did not become ready within the bounded wait. On any theme/apply failure, report the stable code without exposing local paths or credentials.
10. If apply fails after Codex mutation may have started, offer the dedicated offline Restore Skill.
