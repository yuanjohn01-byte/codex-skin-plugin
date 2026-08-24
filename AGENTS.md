# Codex Skin Public Plugin Repository Rules

## Scope

This repository is Public and independently publishable. It owns only the Codex Plugin,
marketplace metadata, Skills, Helper/adapters/Bootstrap/Restorer, generated public
contracts and keys, synthetic fixtures/tests, and public user documentation. The
installable Plugin root is `plugins/codex-skin/`; its directory and manifest name remain
`codex-skin`.

Also follow the Workspace-root `AGENTS.md`. Read Private product/plan material only when
the task needs it, and never copy Private planning or evidence into this repository.

## Public safety boundary

Allowed:

- Plugin and marketplace manifests, Skills and public assets.
- Helper, adapters, Bootstrap, Restorer and their build/release automation.
- Generated allowlisted API/theme/error schemas and verification public keys.
- Synthetic fixtures, tests, README, SECURITY, LICENSE, NOTICE, CHANGELOG and release
  notes.

Forbidden:

- Private website/template source, license proof, repository history or broad copied
  directories.
- Secrets, tokens, cookies, signing private keys, Worker bindings or Production config.
- Customer data, D1/R2 exports, internal logs, private screenshots or local auth state.
- Unreleased/Pro theme packages, source art, rights records, private QA evidence, admin
  APIs, internal tables/rate limits or non-allowlisted contracts.

If an artifact's ownership is uncertain, keep it Private until reviewed. Run the
repository boundary, secret/forbidden-path, license and large-file checks before a
public release.

## Runtime invariants

- Paid Alpha uses Skills plus a finite, on-demand self-contained Helper. Do not add a
  persistent MCP server, daemon, tray/menu app, Guardian or automatic hook without a new
  product/security decision.
- Users must not need Node or Go. Bootstrap selects fixed platform/version assets,
  verifies descriptor/hash/signature and stores Helper/recovery state outside Plugin
  cache.
- Theme data accepts only documented fields and packaged local assets. Reject arbitrary
  CSS/JS/selectors/shell code, remote execution URLs, traversal, symlinks, oversized
  archives and invalid MIME/hash/signature.
- Verify official Codex process identity before attach/stop. CDP is loopback-only.
- Apply follows validate → stage → apply → verify → commit with journal and rollback.
- Compatibility requires identity plus capability/marker probes. Failed or uncertain
  probes block new apply; Restore remains available.
- Offline Restore works while logged out, access expired and Plugin/cache removed.
- Apply, switch, pause, restore and uninstall operations are idempotent where applicable.

## Auth, API and diagnostics

- Consume only generated versioned contracts whose required server behavior is already
  in Production.
- Access tokens stay in memory; refresh tokens use macOS Keychain or Windows Credential
  Manager and rotate on use.
- Respect polling intervals, `Retry-After`, idempotency and non-retryable errors. Never
  infer Pro access from UI, callbacks or editable local state.
- User-facing errors include a stable `CS-*`, an operation/incident ID when available,
  impact and one useful next step; do not expose raw stacks or sensitive paths.
- Diagnostics exclude prompts, code, absolute paths, credentials and screenshots by
  default. Expanded diagnostics need explicit user approval and local redaction preview.

## Distribution and platform support

Release assets are versioned, checksummed, described by an Ed25519-signed descriptor and
accompanied by an SBOM. The current Paid Alpha contract allows distribution without
Apple Developer ID/notarization or Windows commercial signing when fixed-source,
signature/hash, tamper rejection and clear Gatekeeper/SmartScreen disclosures remain.
Do not instruct users to disable system protection. Commercial OS signing remains a
post-launch priority, not a standalone launch gate; an actual OS block still blocks that
platform's RC.

macOS and Windows x64 remain supported Paid Alpha platforms. Shared Helper/adapter or
release changes require relevant automated checks on both and real current-version GUI
evidence before claiming the final release. Documentation or isolated Skill changes do
not trigger unrelated platform suites.

## Verification and release

Discover actual commands from the repository. Match checks to the change: manifest and
marketplace validation; relevant format/vet/test/contract/integration checks; malicious
theme and signature/hash negatives; reproducible multi-platform builds; and no-Node,
network-loss, Plugin-deletion and offline-Restore cases for affected runtime releases.
Keep version, README, CHANGELOG, generated contracts and descriptors consistent.

A Public release stops if its required API/contract is not already in Production. Keep
Private plans, raw evidence and internal release data out of this repository. Detailed
cross-repository ordering and high-impact review live in the Private release workflow,
not in ordinary task startup instructions.
