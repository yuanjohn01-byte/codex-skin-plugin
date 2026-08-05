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
6. After explicit confirmation, run only the matching platform wrapper with `theme launch --json`. This starts one external Runtime Supervisor which owns close, reopen, injection, verification, and session keep-alive. Treat `restartAccepted: true` only as launch acceptance; never run a second apply/launch command and never repeat authorization, purchase, or download.
7. After Codex reopens, run the platform wrapper with `status --json`. If `runtimeStatus` is `starting`, wait briefly and re-check for no longer than 60 seconds. Treat apply as successful only when `restartStatus` is `completed`, `appliedThemePublicId`/`appliedThemeVersion` match the request, and `runtimeStatus` is `active` for that same theme. If launch fails, report only `runtimeErrorCode` or the stable `restartErrorCode`; never convert a desired/downloaded theme into visible success.
8. When no restart is required, treat success only as one JSON result with `ok: true`, the requested `themePublicId`, a `themeVersion`, an `operationId`, and `runtimeStatus: "active"`.
9. After every successful apply, say plainly: “This skin stays active for the current Codex session, including normal renderer reloads. If you completely close Codex or restart the computer, apply it again through Codex Skin next time.” Do not call a completed download or restart acceptance a successful skin session.
10. On `CS-FLOW-AUTH-001`, ask the user to finish or retry authorization. On `CS-FLOW-ACCESS-001`, explain that purchase did not become ready within the bounded wait. `CS-FLOW-VERIFY-001` means the verified runtime could not prove the final renderer state after its bounded wait and one safe reapply; do not immediately retry launch. `CS-FLOW-ROLLBACK-001` means recovery itself was not confirmed; direct the user to Restore before any new apply. Report only the stable code without exposing local paths or credentials.
11. If apply fails after Codex mutation may have started, offer the dedicated offline Restore Skill. Do not ask the user to repeat `theme launch` or reinstall the Plugin.
