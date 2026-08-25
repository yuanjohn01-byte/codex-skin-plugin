# Security Policy

Codex Skin is not publicly released yet. Do not publish secrets or vulnerability details in a public issue.

Before the repository becomes public, a security contact and coordinated disclosure process must be added. Until then, report findings directly to the Founder through the existing private project channel.

The Plugin must never contain website template source, production credentials, private signing keys, customer data, private theme packages or internal operational evidence.

Access Tokens must remain in memory. Refresh Tokens and their device proof may exist only in the current user's macOS Keychain or Windows Credential Manager; they must never enter argv, environment variables, ordinary state files, logs, diagnostics, fixtures, or error text.

Theme packages are data-only and may contain only a strict manifest plus declared local image assets. The Helper rejects arbitrary CSS, JavaScript, shell commands, selectors, remote execution URLs, unsafe archive paths, links, undeclared files, invalid hashes, incompatible minimum engine versions, and invalid or revoked signing keys. Theme signatures are accepted only through the canonical keyset embedded from the generated public contract; caller-supplied or cached replacement keysets fail closed. Only a verified official Codex process may be reached through an exact loopback CDP endpoint.

The Plugin may obtain a Bootstrap launcher only from its fixed release tag and platform filename, and only after its generated SHA-256 pin matches. Pin generation must match one fixed Staging or Production profile; the protected Production profile uses new immutable versions and the exact `https://codexskin.ai` API origin, never relabeled Staging bytes or a free-form protected origin. The launcher accepts Helper release metadata and artifacts only from the fixed Public GitHub Releases origin, verifies them with the embedded canonical Helper release keyset, rejects downgrades, and executes only bounded `version` and `doctor` self-tests before activation. Release signing uses a protected GitHub environment secret; private signing material is never committed or included in an artifact.

Theme state, revalidation material, journals, last-known-good snapshots, and the independent restorer live outside the Plugin cache. Rollback is detached from request cancellation, bounded, verified, and leaves an incomplete mutation journal recoverable. Official appearance restore must remain available without network, login, active access, Node, or an installed Plugin. These Gate B components are unreleased source until the documented review and release gates finish.
