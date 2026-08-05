---
name: codex-skin-status
description: Show local Codex Skin device, pending request, and applied-theme status. Use when the user asks whether Codex Skin is connected, which theme is active, or whether a theme request is pending.
---

# Show Codex Skin status

1. Resolve the Plugin root from this Skill's installed path and use only its platform wrapper:
   - macOS: `scripts/codex-skin.sh status --json`
   - Windows: `scripts/codex-skin.ps1 status --json`
2. Do not call the network. Status reports durable local facts only: whether a device link exists locally, the pending six-digit theme ID if any, the downloaded/committed theme ID/version if any, the Runtime Supervisor status/theme if present, and the bounded launch kind/status/error code if present.
3. Do not describe `deviceLinked: true` as proof that the current server session or Paid Alpha access is valid; that is checked during the next theme operation.
4. A `pending_confirmation` restart is not approved; `restart_approved` or `running` is not completion; only `completed` is terminal success. A `failed` restart must be reported with its stable `restartErrorCode`.
5. Treat `runtimeStatus` as the visible truth. `starting` or `stop_requested` is transitional and must not be called success. `active` means the single external Runtime Supervisor has verified the requested skin in the current official Codex renderer. `ended` means Codex was closed, the computer restarted, or Restore ended that runtime; tell the user to apply again for the next session. `failed` is failure even when `appliedThemePublicId` is present: that field means the signed package was committed locally, not that the skin is visible. Report `runtimeErrorCode` and offer Restore. The legacy `sessionStatus` fields are compatibility aliases only and must not override `runtimeStatus`.
6. Return a short human summary and do not expose the device ID, credentials, absolute paths, recovery contents, request IDs, or operation journals.
