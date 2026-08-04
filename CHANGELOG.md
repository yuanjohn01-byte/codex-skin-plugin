# Changelog

## 0.1.0-paid-alpha - Code-stage candidate

- Make restart consent terminal before renderer mutation so one approved apply performs only the required controlled Codex restart instead of first reopening a throwaway recovery process.
- Reconnect the Scheme A keeper to the exact process identity verified by apply without re-reading the already-restored native appearance setting; use lightweight controller-marker health checks instead of repeated full layout/contrast scans.
- Ignore ordinary conversation DOM mutations in the renderer controller, retaining structural, navigation, root-marker, and bounded safeguard repair paths to reduce typing and pointer latency.
- Recover switch and offline Restore when Chromium has retired the prior CDP session's bootstrap identifier by installing a fixed local neutralizer and verifying immediate official cleanup.
- Add a session-bound renderer controller: a theme is reported active only after the current controlled Codex session has an active controller, renderer reloads/rebuilt routes are rechecked during that session, and Restore asks the controller to stop before restoring the official appearance. Closing Codex or restarting the computer ends the session; no daemon, login item, or automatic reopen is installed.
- Restore the user's native appearance settings on disk before a skin session is reported active, re-verify the live renderer afterward, and reject stale controller heartbeats after an abrupt exit or computer restart.
- Exclude real-current-Codex macOS probes from ordinary `go test`; they require both the `realcodex` build tag and their existing explicit environment opt-in.
- Require the shipped Helper entrypoint to opt in explicitly before CLI orchestration can start a live session controller; injected test flows are safe by default, mutating tests require explicit fake dependencies, and synthetic apply results cannot fall through to the user's recovery Helper or current Codex process.
- Preserve the validated minimal Windows user-profile environment for detached restart/session Helpers so current-profile appearance recovery does not depend on an accidentally inherited shell.
- Export the authoritative device-authorization start and theme release/download v1 contracts from Private.
- Add strict same-origin device authorization start, PKCE proof generation, replay handling, native credential rotation, and one-command continuation.
- Add authenticated theme metadata and bounded binary download clients that reject redirects, cross-origin purchase links, unknown JSON, truncation, oversize, and unexpected content types.
- Continue a six-digit theme request through authorization and an optional bounded Pro purchase wait without asking the user to repeat the request.
- Pass the downloaded package, canonical descriptor, and detached signature to the existing Gate B verifier and transactional engine before apply.
- Persist only a device reference and pending six-digit theme ID outside the Plugin cache; credentials remain in Keychain or Credential Manager and Access Tokens remain memory-only.
- Expose `theme apply`, offline `theme restore`, and local-only `status` Helper commands through dedicated Paid Alpha Skills and fixed platform wrappers.
- Keep the wrapper fail closed until the signed Helper bootstrap has installed its fixed out-of-cache recovery-engine copy.
- Replace the selector-only theme repair with Template v5: exact native appearance backup/pinning, the Codex dropdown token bridge, an engine-owned self-healing renderer controller, Appearance-settings pause/resume, and cleanup on switch/Restore.
- Add Template v6 compatibility for Codex 26.727's CSS-module main-content top fade and fail verification when either the stable or module fade remains visible.
- Reject absent late-rendered activity and diff fixtures as `not_present` instead of passing them, and require computed contrast when those fixtures exist.
- Carry the MIT-licensed lifecycle and native-token mechanisms adapted from Codex Dream Skin v1.5.9 while excluding its artwork and other non-software assets.

This candidate is not a Public or Production release. The API origin, signed Helper Release, Production deployment order, and final macOS/Windows distribution gates remain required before merge/release.

## 0.0.2 - Unreleased

- Bump the read-only distribution spike from v0.0.1 to v0.0.2.
- Keep the Plugin identity and single installation-check Skill stable across the upgrade.
- Add v0.0.1-to-v0.0.2 clean-profile upgrade verification on macOS and Windows CLI environments.
- Add the guarded `main` installation/upgrade flow, JSON verification, restart/new-task check, and non-destructive failure fallback.
- Record successful v0.0.1-to-v0.0.2 Desktop/CLI upgrade checks on macOS and Windows feature refs.
- Enforce the canonical README commands and safety markers with positive and negative repository fixtures.
- Add the self-contained Go Helper source with minimal `version` and runtime-only `doctor` JSONL commands.
- Add the generated Helper protocol v1 Schema and Private-to-Public export digest gate.
- Add reproducible, CGO-free macOS arm64/x64 and Windows x64 Helper test builds.
- Run the Windows x64 Helper with Node, Python, and Go removed from `PATH`.
- Add the generated Helper release descriptor v1 Schema and deterministic unsigned descriptor generator.
- Verify canonical descriptor bytes, detached Ed25519 signatures, strict SemVer/platform selection, and artifact size/SHA-256 before use.
- Add an out-of-Plugin-cache per-user bootstrap with restricted GitHub Release downloads, staged self-test, version directories, and atomic `current.json` activation.
- Preserve the previous Helper, state, and recovery files across failed bootstrap, Plugin cache replacement, and successful upgrade tests.
- Add a secret-free macOS ad-hoc signing/tamper feasibility gate and document the remaining Developer ID, notarization, Gatekeeper, and stapled-container limits.
- Add an ephemeral Windows self-signed Authenticode/tamper/cleanup gate and document the remaining public trust, timestamp, and SmartScreen reputation limits.
- Reject descriptor/signature tampering, artifact mismatch, HTTP truncation, interrupted reads, and downgrade before activation while preserving a reusable last-known-good Helper.
- Add a fixed-surface per-user Guardian spike with deterministic builds, signature/self-test gates, side-by-side upgrade, verified rollback, registration-first uninstall, and native LaunchAgent/Scheduled Task lifecycle checks.
- Add the generated device-authorization poll v1 Schema and an internal, non-CLI Helper client that respects the initial interval, `Retry-After`, persistent `slow_down`, context cancellation, and terminal cancel/expiry states without logging credentials.
- Extend the generated device-authorization contract with one-time token issuance, refresh rotation, replay-revocation errors, and the refresh endpoint while keeping the v0.0.2 Plugin and Helper command surface unchanged.
- Add internal macOS Keychain and Windows Credential Manager backends plus token-response persistence/refresh rotation tests; Access Tokens remain memory-only and the installed v0.0.2 surface remains unchanged.
- Add an internal same-task authorization continuation that strictly validates device-limit management metadata, reuses the same proof after an explicit device-slot signal, and runs the caller's pending operation at most once without changing the installed v0.0.2 surface.
- Add generated Theme Manifest, release descriptor, verification-keyset contracts and the public verification key for the unreleased Gate B engine.
- Add strict data-only package, image, canonical archive/keyset, SHA-256, key-window, and Ed25519 verification with malicious-package rejection and an immutable generated trust root.
- Add official Codex identity verification, exact-loopback CDP discovery, a fixed engine-owned theme template, capability probes, transactional apply/verify/rollback, and durable revalidated theme cache.
- Add Engine v0.2.0 minimum-version enforcement, cancellation-independent verified rollback, crash-journal recovery, last-known-good snapshots, and an out-of-Plugin-cache offline `theme restore` command that requires no network, login, entitlement, Node, or Plugin.
- Add full shared-core macOS/Windows CI coverage while keeping the installed v0.0.2 Plugin read-only.

No theme operation or public compatibility claim is attached to this version.

## 0.0.1 - Unreleased

- Create the local Public Plugin repository boundary.
- Add a validation-ready `codex-skin` Plugin manifest.
- Add the single-entry Git-backed Marketplace catalog and strict metadata validation.
- Add the read-only `codex-skin-version` installation check Skill.
- Add a clean Windows runner smoke test for Marketplace/install/cache integrity.
- Add repository hygiene and public-boundary checks.
- Adopt the MIT license and enforce a minimal Public tracked-file allowlist.

No public release or compatibility claim is attached to this version.
