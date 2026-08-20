# Changelog

## 0.1.0-paid-alpha - Code-stage candidate

- Release the immutable Staging-only candidate: Helper `.12` and Bootstrap `.11`.
  Plugin launcher pins now bind to its signed prerelease and exact per-platform
  Bootstrap SHA-256 values; it is not a stable/latest or Production channel.
- Release the immutable Staging-only candidate: Helper `.11` and Bootstrap `.10`.
  Plugin launcher pins now bind to its signed prerelease and exact per-platform
  Bootstrap SHA-256 values; it is not a stable/latest or Production channel.
- Bind the Plugin's Bootstrap `.9` installer to the signed Staging Helper `.10`
  release at the exact `b580e24617077502fe799562fa193bab1162564f` build closure.
- Prepare immutable Staging Helper `.10` and Bootstrap `.9` artifacts. The
  installed Plugin pins remain on the previous immutable artifacts until the
  signed candidate's three platform hashes are available; a follow-up pin-only
  commit will bind the installer to that exact closure.
- Stop pinning Codex's native light/dark preference during ordinary skin applies. A verified loopback-controlled renderer can now replace any theme, including dark-to-light or light-to-dark, directly; a retained legacy appearance backup is restored once during migration only.
- Let a newly verified user selection replace an unconfirmed restart request, while an approved or running single-use restart remains non-preemptible and reports a stable wait action.
- Replace the restart-worker-to-keeper handoff and all session controller state with one bounded on-demand Helper transaction for apply, direct switch, and Restore.
- Add `theme launch` as the primary restart-confirmation command while retaining `theme continue` only as a compatibility alias.
- Report success only after the exact theme is visibly verified; the Helper then exits and status never reports `runtimeStatus` or `sessionStatus`.
- Add a versioned renderer selector contract pinned to the MIT-licensed Codex Dream Skin v1.5.11 mechanisms, with L1/L2 compatibility tiers and stable data/CSS-module fallbacks.
- Add Template v7 for current Codex header and top-fade data/module selectors, preserving Template v6 as the exact migration/rollback style.
- Add Template v8 route-scoped Home/thread activation: current Home no longer depends on the legacy Composer or thread element before artwork, header, main-boundary, and top-fade rules can apply.
- Add Template v9's conservative task-route fallback: after positively excluding Home and Settings on a verified Codex shell, a page that no longer exposes the legacy thread container is treated as a task/conversation route instead of an unstyled unknown surface. This preserves the existing fail-closed identity, shell, marker, artwork, and contrast checks.
- Treat L2 renderer refinements as reported compatibility diagnostics rather than rolling back a visibly verified L1 shell/theme transaction; retain fail-closed checks for identity, shell anchors, exact style marker, route scope, artwork and contrast.
- Record restart confirmation as a normal pre-mutation pause instead of an `CS-CODEX-IDENTITY-001` failure, and accept a controlled-window shutdown race during rollback only after a stable ordinary Codex process is positively confirmed.
- Replace the persistent Page bootstrap, MutationObserver, route repair, periodic health checks, and controller heartbeat with a current-document-only injector to remove typing-path work and session-lifetime failures.
- Permit direct theme replacement after any historical session/runtime failure; only the current operation's unconfirmed rollback can require Offline Restore.
- Preserve a bounded, redacted terminal-restart history and interrupted engine stage, so a later Restore cannot erase the useful cause of an earlier apply failure. Terminal restart and Restore paths clear stale pending-theme state.
- Require Skills to read current wrapper status rather than infer a failure from a host-restarting launch conversation.
- Recover from macOS LaunchServices dropping Chromium arguments by stopping only the exact verified ordinary process it created, rediscovering the stable signed app, and retrying with the verified executable.
- Make restart consent terminal before renderer mutation so one approved apply performs only the required controlled Codex restart instead of first reopening a throwaway recovery process.
- Exclude real-current-Codex macOS probes from ordinary `go test`; they require both the `realcodex` build tag and their existing explicit environment opt-in.
- Preserve the validated minimal Windows user-profile environment for detached restart Helpers so current-profile appearance recovery does not depend on an accidentally inherited shell.
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
