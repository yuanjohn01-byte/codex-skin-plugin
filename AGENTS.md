# Codex Skin Public Plugin Repository Rules

## Scope

This repository is Public and must remain independently publishable. It owns only the Codex Plugin, marketplace metadata, Skills, Helper/adapters/restorer/bootstrap, public contracts/keys, public fixtures/tests and user-facing Plugin documentation.

The repository root owns marketplace and release metadata. The installable Plugin root is `plugins/codex-skin/`; its folder name and `.codex-plugin/plugin.json` name must both be `codex-skin`.

Also follow the parent Workspace `AGENTS.md`. Read the Private
`docs/product/paid-alpha-release-scope.md` and current project-plan delivery package
without copying Private content here.

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
- Paid Alpha does not install or register Guardian; update-triggered background
  reconcile is deferred.
- Users must not install Node. The released Helper must be self-contained for supported platforms.
- Bootstrap downloads only fixed release assets selected by platform/version, verifies descriptor/hash/signature, and stores Helper/recovery state outside Plugin cache.
- Public release artifacts are versioned, checksummed, covered by an Ed25519 release
  descriptor and accompanied by an SBOM. Invited Paid Alpha users may temporarily
  receive Helpers without commercial OS certificates only with explicit
  Gatekeeper/SmartScreen disclosure. Developer ID/notarization and Windows commercial
  signing are required before unrestricted public self-serve payment.

## Theme and Local Engine Invariants

- Theme/override data accepts only documented whitelisted fields and local packaged assets.
- Reject arbitrary CSS, JS, selectors, Shell, PowerShell, remote execution URLs, traversal, symlinks, oversized archives and invalid MIME/hash/signature.
- Verify Codex official process identity before attach or stop. No process-name-only fallback.
- CDP listens on loopback only; never upload the local port.
- Apply uses validate→stage→apply→verify→commit with journal and last-known-good rollback.
- Compatibility uses verified official identity plus required capability/marker probes
  and post-apply verification. Failed or indeterminate probes prevent new apply;
  an unseen version number alone does not. Restore always remains available.
- Restore must work offline, logged out, Paid Alpha access expired and Plugin removed.
  It must live outside Plugin cache.
- Repeated switch/pause/restore/uninstall operations must be idempotent.

## API, Auth and Diagnostics

- Consume only generated, versioned public contracts from the Private allowlist.
- Access tokens stay in memory; refresh tokens use macOS Keychain/Windows Credential Manager and rotate on use.
- Respect server polling interval, `Retry-After`, idempotency and non-retryable errors.
- Do not locally grant Pro access based on UI, callback URL or editable state. Server
  access state is authoritative.
- User-facing failures include a stable `CS-*`, one `INC-*` or local operation ID, current-theme impact and an actionable next step. Do not show raw stack traces.
- Default diagnostics exclude prompts, code, absolute paths, tokens, cookies and screenshots. Expanded diagnostics require explicit user approval and local redaction preview.

## Task entry

Follow the Workspace-root `AGENTS.md` for task contracts, risk handling and remote
delivery, and the Private release workflow for cross-repository sequencing. Confirm
the Private project-plan package has `repo_scope: plugin` or `both`, inspect the manifest,
marketplace, generated contracts and tests, and preserve unrelated work. Never patch
the read-only Dream Skin reference. If an API is not in Production, stop a Plugin
release even when local checks pass.

## Verification and Release

Minimum relevant checks:

- manifest/marketplace/schema validation.
- format/lint/typecheck/unit/contract/integration tests available in the repo.
- secret, forbidden-path, proprietary-template/source marker, license, dependency, SBOM and large-file scans.
- malicious theme and signature/hash negative tests.
- macOS and Windows fresh install, vN→vN+1 update, apply, verify, switch and restore
  for shared Helper/adapter or Release changes; ordinary Skill/docs changes stay scoped.
- no-Node environment, network loss, Plugin deletion, token revoke and offline restore.
- version, README, CHANGELOG, generated contracts and release descriptor consistency.

Do not invent command names. Once CI/scripts exist, update this file with the verified commands.

Keep only durable public user/safety/release documentation here; never add Private
plans/evidence or raw local artifacts. The Workspace root and Private release workflow
define profiles, review, PR CI, release order and Founder-confirmation boundaries.

The required `repository-boundary` context always reports. Durable docs and fixture
validation do not install Go or trigger Helper/Guardian/signing/platform matrices.
Guardian and signing feasibility remain available as deferred/manual workflows but
are not Paid Alpha gates except when their own code changes. `PA-CI-001` must align the
central manual profile before RC; a proven normal merge-main stays lightweight.
