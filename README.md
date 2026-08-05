# Codex Skin Plugin

This is the standalone Public Plugin repository for Codex Skin. The installable Plugin root is `plugins/codex-skin/`; the repository root contains the Git-backed Marketplace metadata and release documentation.

Current status: this feature branch contains the unreleased Runtime v2 refactor for the `0.1.0-paid-alpha` Plugin line. It keeps the Plugin as the user entry while one external Runtime Supervisor owns launch, renderer injection, visible verification, in-session switching, and session keep-alive. It is not a Public or Production release. The release workflow must not publish it until an immutable signed Helper candidate and the documented macOS/Windows gates are complete.

The candidate consumes only allowlisted contracts exported from Private. One six-digit theme request can continue through same-origin device authorization, an optional bounded Pro purchase wait, verified package download, the existing Gate B signature/package checks, and transactional apply. Refresh credentials remain in macOS Keychain or Windows Credential Manager; Access Tokens remain memory-only. Offline Restore continues to work from the fixed out-of-cache recovery engine without login, active access, network, Node, or the Plugin cache.

The v0.0.1-to-v0.0.2 upgrade spike has passed macOS and Windows Desktop/CLI checks against the reviewed feature refs. The Windows distribution workflow also performs the equivalent CLI/cache upgrade on a clean GitHub-hosted runner. Every release still requires a post-merge two-platform check of the exact `main` form before its commands are published.

The repository uses the MIT license. Its tracked-file allowlist, secret/Private-path checks and negative fixtures must pass before every remote push. Founder approval to create the Public repository has been recorded.

## Current structure

```text
codex-skin-plugin/
  AGENTS.md
  LICENSE
  .agents/
    plugins/
      marketplace.json
  plugins/
    codex-skin/
      .codex-plugin/plugin.json
      skills/
        codex-skin-version/SKILL.md
        codex-skin-install-theme/SKILL.md
        codex-skin-restore-theme/SKILL.md
        codex-skin-status/SKILL.md
      scripts/
      assets/
  cmd/codex-skin/              # self-contained Helper entrypoint
  cmd/codex-skin-guardian/     # fixed-surface internal Guardian spike
  internal/                    # Helper, theme engine, adapter, auth, and Guardian packages
  contracts/                   # generated public contracts only
  tools/
    validate_public_repo.py
    test_public_repository.py
```

The candidate Skills expose version, apply, local status, and offline restore. On the first `theme apply`, their wrappers may download only the release-tagged Bootstrap launcher whose platform filename and SHA-256 are pinned in the Plugin. That launcher verifies the signed Helper release and installs it under the per-user Codex Skin recovery path. All Helper commands then invoke only that fixed external copy; they never select a binary from the replaceable Plugin cache. `status`, `theme launch` (and its temporary `theme continue` compatibility alias), and Restore never bootstrap or use the network and fail closed when the verified external Helper is absent.

The Helper's API origin is a build-time value and is intentionally empty in ordinary source builds. This prevents an installed Plugin from accepting an arbitrary server URL or depending on an undeployed endpoint. Staging/Production artifacts must pin the approved HTTPS origin during their release build and pass the corresponding environment gate.

The last immutable Scheme A Staging artifact is Helper `0.1.0-paid-alpha.4` with Bootstrap `0.1.0-paid-alpha.3`; Runtime v2 deliberately does not reuse or overwrite those bytes. Runtime v2 is built as the new Helper `0.1.0-paid-alpha.5` with Bootstrap `0.1.0-paid-alpha.4`; those revisions are source-candidate identities until the exact Staging-origin artifacts are signed and published. Production promotion requires another immutable revision, the approved Production origin, and the release gate for those exact artifacts; the Plugin version remains `0.1.0-paid-alpha` during the invited Alpha line.

## Helper development

Go 1.26.5 is pinned in `go.mod`. The minimal contract checks are:

```bash
go test ./...
go vet ./...
go run ./cmd/codex-skin version --json
go run ./cmd/codex-skin doctor --json
go run ./cmd/codex-skin status --json
go run ./cmd/codex-skin theme restore --json
python3 tools/test_helper_builds.py
python3 tools/test_release_descriptor.py
python3 tools/test_guardian_builds.py
```

The canonical Helper protocol, release descriptor, device-authorization, and theme-download Schemas live in the Private repository allowlist and are generated into `contracts/`. Direct edits to a Public Schema or its digest manifest fail the repository boundary check.

The credential backend uses the fixed `/usr/bin/security` binary on macOS and sends the secret only through stdin; it calls the current user's native `CredWriteW`/`CredReadW`/`CredDeleteW` APIs directly on Windows and frees the returned credential buffer. It never uses a shell, `cmdkey`, argv, environment variables, or an ordinary state file for credential contents. Native tests use an isolated temporary Keychain on macOS and a synthetic, cleanup-guarded Generic Credential on the Windows hosted runner.

The build test produces unsigned internal artifacts for `macos-arm64`, `macos-x64`, and `windows-x64` under ignored `dist/helper/`, validates Mach-O/PE architecture headers, and compares two clean builds byte-for-byte. Release assets are not committed to Git. Windows CI executes the native x64 Helper after removing Node, Python, and Go from `PATH`.

`tools/create_release_descriptor.py` converts that trusted build summary into one canonical, fixed-order descriptor with the exact version, tag, UTC timestamp, platform filenames, sizes, and SHA-256 values. The Go release package rejects noncanonical JSON, unknown fields or signing key IDs, invalid detached Ed25519 signatures, missing/duplicate/mismatched platforms, unsupported runtimes, and downloaded bytes with the wrong size or digest. Tests generate ephemeral signing keys at runtime; this repository contains no release private key or Production trust-root claim. The S3 artifact remains an unsigned internal review artifact until the later signing and release gates are complete.

The bootstrap library uses the fixed Public GitHub Releases origin, accepts only `helper-release-descriptor.json`, its raw detached Ed25519 signature, and strict Helper filenames, and allows HTTPS redirects only to GitHub release-asset hosts. The verification keyset is generated from the canonical Private allowlist and embedded into the launcher. After signature/platform/size/SHA-256 verification it writes a per-version executable in `~/Library/Application Support/CodexSkin/bin/` on macOS or `%LOCALAPPDATA%\CodexSkin\bin\` on Windows, runs only the fixed `version --json` and `doctor --json` self-tests with a minimal environment, updates the cache-independent recovery engine transactionally, and only then atomically replaces `current.json`. The install result reports the exact Helper and recovery SHA-256 values. Descriptor/signature tampering, wrong artifact bytes, declared-length truncation, reader interruption, downgrade, self-test failure, and current activation failure all stop safely; the latter also restores the prior recovery engine. Untrusted candidates never reach the executable self-test, and the previous pointer and Helper remain reusable without staging debris. The application root must not overlap or resolve through the Plugin cache; tests replace that cache and confirm the Helper plus `state/` and `recovery/` sentinels remain. This is still pre-release infrastructure: no unsigned artifact is authorized for user installation.

The Gate B engine accepts only versioned manifest fields and declared local PNG/JPEG/WebP assets; theme packages cannot provide CSS, JavaScript, shell commands, selectors, or remote execution URLs. The engine transaction follows `validate → stage → backup → apply → verify → commit`; restart consent is terminal before mutation. After confirmation, the detached Helper process becomes the single Runtime Supervisor: it stops only the exact verified official Codex instance, rediscovers a stable signed installation, launches the same user profile on loopback CDP, waits for a real renderer, installs the watcher, verifies the exact theme, restores the original on-disk appearance bytes, and only then commits visible success. There is no second controller handoff. During an active runtime, a signed theme switch uses the same Codex PID and Runtime PID without another restart; foreground apply and runtime health checks share a bounded operation lock so they cannot inspect a half-switched renderer. Status treats `runtimeStatus: active` as the visible truth—downloaded or desired state alone is never success.

The renderer compatibility layer pins the MIT-licensed selector lessons from Codex Dream Skin v1.5.11 in one Helper-owned contract. L1 shell anchors use stable data attributes, semantic roles, stable classes, and bounded CSS-module prefixes; missing L2 refinements degrade without rolling back the whole skin. Template v7 covers the current header and top-fade data/module contracts. The new-document bootstrap, route watcher, target rediscovery, and low-frequency health probe repair renderer reloads without scanning every streaming message mutation, keeping the watcher off the typing hot path. Before activation, the Runtime restores the user's original native appearance settings and re-verifies the running renderer, so an abrupt shutdown cannot pin the next ordinary Codex launch to the skin's light/dark mode. It never creates a login item, service, daemon, tray app, or auto-launch behavior: completely closing Codex or restarting the computer ends the skin runtime, so the user applies again next time. Restore removes the watcher, fixed theme marker and background without network, login, access entitlement, Node, or Plugin cache access; if Chromium retired a previous script identifier, a fixed local neutralizer still provides verified cleanup. If apply, verify, runtime activation, switch, or restore fails, rollback uses a bounded context detached from request cancellation and preserves a recoverable journal. Formal user exposure remains deferred to the later signed Staging and release gates.

The [macOS signing feasibility note](docs/macos-signing-feasibility.md) and its CI workflow test ad-hoc signing, strict verification, and post-signing tamper rejection without using secrets. Ad-hoc signatures are explicitly not Developer ID signatures or notarization; formal macOS distribution remains blocked on a protected Apple certificate, accepted notarization, the exact Gatekeeper download path, and a decision about a staplable release container.

The [Windows signing feasibility note](docs/windows-signing-feasibility.md) uses a one-run, non-exportable self-signed certificate only inside the current-user CI stores to test Authenticode signing, local-policy verification, signed Helper execution, PE tamper rejection, and certificate cleanup. It uploads only a non-secret JSON summary. Self-signing does not provide public trust or SmartScreen reputation; formal Windows distribution remains blocked on a protected public code-signing identity, RFC 3161 timestamp, and clean-machine testing of the exact final download channel.

The [per-user Guardian lifecycle note](docs/guardian-lifecycle-feasibility.md) describes the internal fixed-surface Guardian and its versioned install, signature gate, per-user registration, side-by-side upgrade, explicit rollback, and registration-first uninstall tests. Native macOS LaunchAgent and Windows Limited/Interactive Scheduled Task jobs create, run, inspect, and remove temporary registrations without adding a service, network listener, or general command surface. The trigger remains a packaging-only Spike; actual lifecycle reconciliation is a later numbered task, and formal Guardian distribution remains blocked on the same platform signing gates.

## Release installation contract

The following is the single installation flow for releases on `main`. It is retained as the release contract, not as permission to install this unmerged code-stage branch. A release is ready for website publication only after its documented gates pass. Users do not need to open or fill in the Marketplace form, edit Codex configuration, or delete cache files.

Run these commands in a terminal:

```bash
codex plugin marketplace add yuanjohn01-byte/codex-skin-plugin --ref main
codex plugin add codex-skin@codex-skin
codex plugin list --json
```

The final command must show exactly one installed `codex-skin@codex-skin` entry with `installed: true` and `enabled: true`. Completely quit Codex, reopen it, start a new task, and ask Codex to run `$codex-skin-version`. For the Paid Alpha release, the Skill must report `0.1.0-paid-alpha`; apply/restore/status are accepted only after the signed Helper and exact API origin are also verified.

The command shape has passed macOS and Windows Desktop/CLI tests against the reviewed feature refs. For every release, publishing the `main` form also requires a post-merge two-platform check.

## Upgrade

Refresh the existing Git-backed Marketplace snapshot, reinstall the same Plugin ID, and verify the result:

```bash
codex plugin marketplace upgrade codex-skin
codex plugin add codex-skin@codex-skin
codex plugin list --json
```

Then completely quit Codex, reopen it, and run `$codex-skin-version` in a new task. A stale version subtitle in the Plugin details view is not enough to diagnose a failed upgrade: compare the JSON result and the new-task Skill result first.

## Failure fallback

If Marketplace add/upgrade or Plugin add fails, stop before making manual changes. Keep an already installed Plugin in place, and save the failing command plus its original error. Collect only these shareable diagnostics:

```bash
codex --version
codex plugin marketplace list
codex plugin list --json
```

Do not share tokens, cookies, account data, prompts, source code, or absolute user paths. Do not edit Codex configuration or delete Marketplace/Plugin cache directories.

If the `codex-skin` Marketplace snapshot alone is missing or stale, refresh it with the reversible fallback below; this does not remove an installed Plugin:

```bash
codex plugin marketplace remove codex-skin
codex plugin marketplace add yuanjohn01-byte/codex-skin-plugin --ref main
codex plugin add codex-skin@codex-skin
codex plugin list --json
```

If an existing Plugin still works but the upgrade does not, leave it installed and report the diagnostics instead of uninstalling it. Do not invoke an unsigned internal Helper build as a product release.

## License

Codex Skin Plugin is available under the [MIT License](LICENSE). Third-party components and assets remain subject to their own applicable licenses and notices.
