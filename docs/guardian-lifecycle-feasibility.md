# Per-User Guardian Lifecycle Feasibility

This S3 spike validates packaging and lifecycle mechanics for a minimal Skin Guardian. It does not expose the Guardian through the installed Plugin and does not implement Codex observation or theme reconciliation.

## Fixed surface

The Guardian binary is self-contained Go and has only two internal contracts:

- `version --json` for installer self-test.
- `run --reason <enum> --json --internal-spike`, where the only accepted reasons are `renderer`, `process`, `version`, `rule-refresh`, and `manual`.

The spike `run` command reports that the trigger was validated and that reconcile is not implemented. It does not import a network client/server, open a socket, accept a path/URL/theme/command, read credentials, start a shell, download an asset, or touch Codex. The fixed Helper `lifecycle reconcile`, lock, event deduplication, and bounded retry behavior remain assigned to `M1-S6-013`.

## Versioned lifecycle

The lifecycle manager requires an injected platform-signature verifier and a fixed `version --json` self-test before registration. It then:

1. checks strict SemVer, size, and SHA-256;
2. writes a side-by-side version in the per-user application root outside the Plugin cache;
3. invokes the signature verifier and fixed self-test;
4. installs a fixed registration pinned to that verified absolute path;
5. atomically replaces `guardian/current.json`.

An upgrade never overwrites the running version. A verifier, self-test, or registration failure preserves the prior registration and current pointer and removes the failed candidate. Explicit rollback re-verifies the installed older binary before repinning the registration. Uninstall removes the registration first, deletes only `guardian/`, and leaves Helper `bin/`, theme state, and recovery data intact.

## Per-user registration

macOS uses a per-user LaunchAgent with `RunAtLoad`, `KeepAlive=false`, `ProcessType=Background`, and the Aqua session. Windows uses a per-user Scheduled Task with `InteractiveToken`, `LeastPrivilege`, `IgnoreNew`, no network requirement, and a five-minute execution limit. Both registrations contain only the verified version path and the fixed internal process trigger. They do not install a root/SYSTEM service, request highest privilege, or invoke Shell/PowerShell.

Native CI creates, runs, inspects, and removes a temporary registration on both platforms. Tests also prove descriptor removal and preservation of unrelated Helper/state/recovery sentinels.

## Signing boundary

The macOS job ad-hoc signs both lifecycle versions, performs strict `codesign` verification, and rejects a modified Mach-O. The Windows job attaches a non-exportable, one-day self-signed Authenticode certificate, requires signer presence, and requires `HashMismatch` after PE modification. The ephemeral Windows certificate and both temporary OS registrations are removed in `finally` cleanup.

These are internal feasibility checks only. Ad-hoc signing is not Developer ID signing or notarization. Self-signed Authenticode does not provide public trust, RFC 3161 timestamping, or SmartScreen reputation. No Guardian is ready for public distribution until the existing formal signing gates close.
