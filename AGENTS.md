# Codex Skin Public Plugin Repository Rules

## Scope

This repository is Public and must remain independently publishable. It owns only the Codex Plugin, marketplace metadata, Skills, Helper/adapters/restorer/bootstrap, public contracts/keys, public fixtures/tests and user-facing Plugin documentation.

The repository root owns marketplace and release metadata. The installable Plugin root is `plugins/codex-skin/`; its folder name and `.codex-plugin/plugin.json` name must both be `codex-skin`.

Also follow the parent Workspace `AGENTS.md`. For cross-repository tasks, read the Private project-plan item without copying Private content here.

## Public Safety Boundary

Allowed:

- `plugins/codex-skin/.codex-plugin/plugin.json` and Git-backed marketplace metadata.
- Skills for install, authorize, apply, switch, customize, pause, restore, uninstall, status and diagnostics.
- Helper source/binaries release workflow, platform adapters, validator and independent restorer.
- Generated public API/theme/error schemas and signature verification public keys.
- Synthetic fixtures, tests, README, SECURITY, LICENSE, CHANGELOG and public release notes.

Forbidden:

- Any Private website/template source, package archive, license proof or derivative template page.
- Private repository history or broad copied directories.
- `.env`, secrets, tokens, cookies, signing private keys, Workers bindings or Production config.
- User/customer data, real D1/R2 exports, internal logs, private screenshots or local auth state.
- Unreleased theme packages, Pro packages, source art, commercial rights records or private QA evidence.
- Admin APIs, internal database schemas, internal rate limits or non-allowlisted contracts.

Run a secret, forbidden-path, license and large-file scan before every push/release. If uncertain, stop and keep the artifact Private until reviewed.

## Plugin Structure and Runtime

- Follow the current official Codex Plugin structure, including required `plugins/codex-skin/.codex-plugin/plugin.json`.
- Website install/update instructions must be copied only from macOS/Windows `PLG-S1` evidence, not guessed from old screenshots or memory.
- MVP uses Skills plus an on-demand CLI Helper. Do not add a persistent MCP server, daemon, tray/menu app or automatic hook without a new architecture/security decision.
- Users must not install Node. The released Helper must be self-contained for supported platforms.
- Bootstrap downloads only fixed release assets selected by platform/version, verifies descriptor/hash/signature, and stores Helper/recovery state outside Plugin cache.
- Public release artifacts are versioned, checksummed, signed/notarized as required and accompanied by an SBOM.

## Theme and Local Engine Invariants

- Theme/override data accepts only documented whitelisted fields and local packaged assets.
- Reject arbitrary CSS, JS, selectors, Shell, PowerShell, remote execution URLs, traversal, symlinks, oversized archives and invalid MIME/hash/signature.
- Verify Codex official process identity before attach or stop. No process-name-only fallback.
- CDP listens on loopback only; never upload the local port.
- Apply uses validate→stage→apply→verify→commit with journal and last-known-good rollback.
- Unknown/blocked compatibility prevents new apply but never prevents restore.
- Restore must work offline, logged out, subscription expired and Plugin removed. It must live outside Plugin cache.
- Repeated switch/pause/restore/uninstall operations must be idempotent.

## API, Auth and Diagnostics

- Consume only generated, versioned public contracts from the Private allowlist.
- Access tokens stay in memory; refresh tokens use macOS Keychain/Windows Credential Manager and rotate on use.
- Respect server polling interval, `Retry-After`, idempotency and non-retryable errors.
- Do not locally grant Pro access based on UI, callback URL or editable state. Server entitlement is authoritative.
- User-facing failures include a stable `CS-*`, one `INC-*` or local operation ID, current-theme impact and an actionable next step. Do not show raw stack traces.
- Default diagnostics exclude prompts, code, absolute paths, tokens, cookies and screenshots. Expanded diagnostics require explicit user approval and local redaction preview.

## Start and Progress

1. Confirm the Private project-plan task has `repo_scope: plugin` or `both` and is the only `开发中` item.
2. Inspect branch, remotes, `git status`, manifest, marketplace, release workflow, generated contracts and tests.
3. Record the planned platforms, versions, QA IDs and evidence location.
4. Preserve unrelated work; never patch the read-only Dream Skin reference.
5. If the required Private API is not already in Production, stop the Plugin release even if local tests pass.

The Private task-start record is maintained locally and does not require a standalone Public push. Push only a locally verified, reviewable Public increment, or open a Draft PR early when remote cross-platform CI or collaboration is needed.

Update the Private project plan at task end with Public branch, commit, PR, tag/release, checksums, platform run IDs and remaining issues.

## Verification and Release

Minimum relevant checks:

- manifest/marketplace/schema validation.
- format/lint/typecheck/unit/contract/integration tests available in the repo.
- secret, forbidden-path, proprietary-template/source marker, license, dependency, SBOM and large-file scans.
- malicious theme and signature/hash negative tests.
- macOS and Windows fresh install, vN→vN+1 update, apply, verify, switch and restore.
- no-Node environment, network loss, Plugin deletion, token revoke and offline restore.
- version, README, CHANGELOG, generated contracts and release descriptor consistency.

Do not invent command names. Once CI/scripts exist, update this file with the verified commands.

Use `codex/<task-id>-<slug>` branches and `type(scope): summary` commits. When a verified task/subtask is complete, update the project plan, commit, push the feature branch and create/update a PR without another reminder.

Public feature CI is PR-driven; do not restore a parallel `push` trigger for `codex/**` or push solely for task status/evidence. Stale PR heads are cancelled, the Public baseline checks `main` after merge, and path-filtered platform workflows remain available on PRs and by manual dispatch.

Public release sequence:

```text
PR checks → two-platform install/update/apply/restore
→ bump plugin version + CHANGELOG → merge
→ immutable plugin-vX.Y.Z tag → GitHub Release
→ Private recommended_version → verify public update instructions
```

Never force-push or publish failing/unsigned Production artifacts. Creating the Public remote, changing visibility/license, or forcing a mass minimum version requires explicit Founder confirmation.
