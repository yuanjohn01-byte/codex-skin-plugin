---
name: codex-skin-status
description: Show local Codex Skin device, pending request, and applied-theme status. Use when the user asks whether Codex Skin is connected, which theme is active, or whether a theme request is pending.
---

# Show Codex Skin status

1. Resolve the Plugin root from this Skill's installed path and use only its platform wrapper:
   - macOS: `scripts/codex-skin.sh status --json`
   - Windows: `scripts/codex-skin.ps1 status --json`
2. Do not call the network. Status reports durable local facts only: whether a device link exists locally, the pending six-digit theme ID if any, the applied theme ID/version if any, and the bounded restart continuation kind/status/error code if present.
3. Do not describe `deviceLinked: true` as proof that the current server session or Paid Alpha access is valid; that is checked during the next theme operation.
4. A `pending_confirmation` restart is not approved; `restart_approved` or `running` is not completion; only `completed` is terminal success. A `failed` restart must be reported with its stable `restartErrorCode`.
5. Return a short human summary and do not expose the device ID, credentials, absolute paths, recovery contents, request IDs, or operation journals.
