---
name: codex-skin-status
description: Show local Codex Skin device, pending request, and last verified theme status. Use when the user asks whether Codex Skin is connected, which theme was last applied, or whether a theme request is pending.
---

# Show Codex Skin status

1. Resolve the Plugin root from this Skill's installed path and use only its platform wrapper:
   - macOS: `scripts/codex-skin.sh status --json`
   - Windows: `scripts/codex-skin.ps1 status --json`
2. Do not call the network. Status reports durable local facts only: whether a device link exists locally, the pending six-digit theme ID if any, the last package whose one-time operation committed after visible verification, and the bounded launch kind/status/error code if present.
3. Do not describe `deviceLinked: true` as proof that the current server session or Paid Alpha access is valid; that is checked during the next theme operation.
4. A `pending_confirmation` restart is not approved; `restart_approved` or `running` is not completion; only `completed` is terminal success. A `failed` restart must be reported with its stable `restartErrorCode`.
5. On-demand mode has no `runtimeStatus` or session keeper. `appliedThemePublicId` means the last on-demand operation completed its visible verification; it does not promise that a later renderer reload remains themed. A historical operation failure must not force Restore before a new apply. A completed Restore clears any prior pending theme selection. Report a failed current restart with its stable `restartErrorCode`; offer Restore only when the current failure code says rollback or official recovery itself was not confirmed.
6. Return a short human summary and do not expose the device ID, credentials, absolute paths, recovery contents, request IDs, or operation journals.
