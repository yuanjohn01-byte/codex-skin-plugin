# Security Policy

Codex Skin is not publicly released yet. Do not publish secrets or vulnerability details in a public issue.

Before the repository becomes public, a security contact and coordinated disclosure process must be added. Until then, report findings directly to the Founder through the existing private project channel.

The Plugin must never contain website template source, production credentials, private signing keys, customer data, private theme packages or internal operational evidence.

Access Tokens must remain in memory. Refresh Tokens and their device proof may exist only in the current user's macOS Keychain or Windows Credential Manager; they must never enter argv, environment variables, ordinary state files, logs, diagnostics, fixtures, or error text.
