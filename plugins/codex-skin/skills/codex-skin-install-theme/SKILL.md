---
name: codex-skin-install-theme
description: Apply a published Codex Skin theme by its six-digit theme ID. Use when the user asks to install, apply, try, or switch to a Codex Skin theme.
---

# Apply a Codex Skin theme

1. Obtain exactly one six-digit theme ID from the user or the selected Codex Skin catalog item. Never invent an ID.
2. Resolve the Plugin root from this Skill's installed path, then use only the platform wrapper inside `scripts/`. On the first apply or a verified upgrade, this same fixed wrapper installs the signed Helper automatically; never download or copy a Helper manually:
   - macOS: `scripts/codex-skin.sh theme apply THEME_ID --json`
   - Windows: `scripts/codex-skin.ps1 theme apply THEME_ID --json`
3. Do not call Codex Skin HTTP endpoints directly and do not accept a custom API origin, download URL, package path, selector, CSS, JavaScript, or shell fragment.
4. Allow the command to stay active while it opens the same-origin authorization page and, for a Pro theme without access, the same-origin Pricing page. The command resumes automatically; do not ask the user to repeat the theme request.
5. If the command returns `CS-FLOW-RESTART-001` with action `confirm_restart`, explain that the current Codex window must close and reopen once so the verified Helper can use this same Codex profile. Ask for an explicit yes/no confirmation. Do not run the continuation command until the user says yes.
6. After explicit confirmation, run only the matching platform wrapper with `theme launch --json`. This starts one on-demand Helper operation: it owns close, reopen, injection, and visible verification, then exits. Treat `restartAccepted: true` only as launch acceptance; never run a second apply/launch command and never repeat authorization, purchase, or download.
7. After Codex reopens, run the platform wrapper with `status --json`. If `restartStatus` is `running`, wait briefly and re-check for no longer than 60 seconds. Treat apply as successful only when `restartStatus` is `completed` and `appliedThemePublicId`/`appliedThemeVersion` match the request. Do not call a desired/downloaded package or restart acceptance a successful skin.
8. A previous completed or failed on-demand operation never requires Restore before a user requests another theme. A same-mode replacement in the still-verified controlled Codex window completes directly. A light/dark change, renderer reload, or untrusted current window may need one restart, but it must not insert a Restore or a second restart. A new selection replaces an unconfirmed restart request; if a restart is already approved or running, report `CS-FLOW-RESTART-002` and wait rather than starting another operation.
9. When no restart is required, treat success only as one JSON result with `ok: true`, the requested `themePublicId`, a `themeVersion`, and an `operationId`.
10. After every successful apply, say plainly: “This skin has been visibly verified in the current Codex window. If you completely close Codex or restart the computer, apply it again through Codex Skin next time.” Do not promise automatic repair after a later renderer reload.
11. On `CS-FLOW-AUTH-001`, ask the user to finish or retry authorization. On `CS-FLOW-ACCESS-001`, explain that purchase did not become ready within the bounded wait. `CS-FLOW-VERIFY-001` means the one-time Helper could not prove the final renderer state; the user may retry the requested apply. `CS-FLOW-ROLLBACK-001` means recovery itself was not confirmed; direct the user to Restore before any new apply. Report only the stable code without exposing local paths or credentials.
